import json
import base64
import math
from pathlib import Path
import re
import time
import uuid

from run import HERE, ROOT, Run, Stop, digest

PODMAN = ["podman", "--remote=false"]
MEMORY = 1024 * 1024 * 1024
OUTPUT_LIMIT = 2 * 1024 * 1024
REPOSITORY_GOFLAGS = "GOFLAGS=-mod=readonly -p=1 -trimpath"
ENVIRONMENT = {
    "PATH": "/usr/local/go/bin:/usr/bin:/bin", "HOME": "/tmp", "GOCACHE": "/tmp/go-build",
    "GOPATH": "/go", "GOPROXY": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
    "GOENV": "off", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0", "PYTHONDONTWRITEBYTECODE": "1",
}
NATIVE_ENVIRONMENT = {**ENVIRONMENT, "GOFLAGS": REPOSITORY_GOFLAGS.removeprefix("GOFLAGS=")}


def repository_command(task):
    targets = ["test", "interactions"] if task["id"] == "viewer-modal-v1" else ["test"]
    return ["make", *targets, "BUILD_VCS=false", "COMMIT=" + task["base_commit"], REPOSITORY_GOFLAGS]


def retain_viewer_artifacts(output, directory=None):
    artifacts = {}
    total = 0
    text = []
    for raw in output.splitlines():
        try:
            event = json.loads(raw)
        except (ValueError, UnicodeDecodeError):
            continue
        if isinstance(event, dict) and isinstance(event.get("Output"), str):
            text.append(event["Output"])
    for line in "".join(text).splitlines():
        line = line.strip()
        if not line.startswith("HARNESS_VIEWER_ARTIFACT "):
            continue
        try:
            item = json.loads(line.removeprefix("HARNESS_VIEWER_ARTIFACT "))
            name = item["name"]
            if not isinstance(name, str) or not re.fullmatch(r"[a-z][a-z0-9-]{0,40}/[a-z][a-z0-9-]{0,50}\.(png|svg|txt|json|jsonl|gpx)", name):
                raise ValueError("Invalid artifact name")
            if name in artifacts or len(artifacts) >= 64:
                raise ValueError("Duplicate or excessive artifacts")
            content = base64.b64decode(item["data"], validate=True)
            total += len(content)
            if len(content) > 512 * 1024 or total > OUTPUT_LIMIT:
                raise ValueError("Artifact size limit exceeded")
        except (ValueError, KeyError, TypeError) as error:
            raise Stop("blocked", "Viewer capture output is invalid") from error
        if directory is not None:
            target = directory / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
        artifacts[name] = digest(content)
    return artifacts


class ContainerRun(Run):
    def __init__(self, reports, image):
        super().__init__(reports)
        self.image = image
        self.seccomp = None
        self.prepared = False
        self.hooks = self.path / "empty-hooks"
        self.hooks.mkdir()
        self.task = json.loads((HERE / "task.json").read_text())
        self.test_command = self.task["command"]
        self.result.update(execution_mode="rootless-podman", image=image, containers=[],
                           container_runner_sha256=digest(Path(__file__).read_bytes()))

    def prepare(self):
        self.prepared = False
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", self.image):
            raise Stop("invalid_input", "Use a full immutable local image ID: sha256:<64 hex characters>")
        self.seccomp = self.result["runtime"].get("seccomp_profile")
        if not self.seccomp or not Path(self.seccomp).is_file():
            raise Stop("blocked", "The reported default seccomp profile is unavailable")
        self.result["seccomp_sha256"] = digest(Path(self.seccomp).read_bytes())
        check, output = super().command("image-inspect", PODMAN + ["image", "inspect", self.image], ROOT)
        try:
            images = json.loads(output)
            identity = images[0]["Id"].removeprefix("sha256:")
            if check["exit_code"] or len(images) != 1 or identity != self.image.removeprefix("sha256:"):
                raise ValueError("Image mismatch")
        except (ValueError, KeyError, IndexError, TypeError, AttributeError) as error:
            raise Stop("blocked", "The exact prepared image is unavailable locally") from error
        self.result["image_repo_digests"] = images[0].get("RepoDigests", [])
        self.prepared = True

    def validate_container(self, data, checkout, build_output=False, *, native_workspace=False):
        try:
            if build_output and native_workspace:
                raise ValueError("Conflicting workspace modes")
            item = data[0]
            config = item["HostConfig"]
            expected = {"NetworkMode": "none", "ReadonlyRootfs": True, "Privileged": False,
                        "PidMode": "private", "IpcMode": "private", "UsernsMode": "private", "Memory": MEMORY,
                        "CgroupMode": "private", "UTSMode": "private",
                        "MemorySwap": MEMORY, "CpuPeriod": 100000, "CpuQuota": 200000,
                        "PidsLimit": 256}
            if any(config[key] != value for key, value in expected.items()):
                raise ValueError("Container restrictions differ from the requested policy")
            if item["Image"].removeprefix("sha256:") != self.image.removeprefix("sha256:"):
                raise ValueError("Container image changed")
            if item["BoundingCaps"] not in ([], None) or item["EffectiveCaps"] not in ([], None):
                raise ValueError("Container retains capabilities")
            if config["Annotations"]["io.podman.annotations.userns"] != "nomap":
                raise ValueError("Container user namespace changed")
            # Podman's outer IDs are intermediate IDs; the invoking UID/GID is 0 there.
            for mapping in (config["IDMappings"]["UidMap"], config["IDMappings"]["GidMap"]):
                if not mapping:
                    raise ValueError("Missing user namespace mapping")
                for entry in mapping:
                    inside, outside, count = map(int, entry.split(":"))
                    if inside < 0 or outside <= 0 or count <= 0:
                        raise ValueError("The real rootless user must not be mapped into the container")
            if "no-new-privileges" not in config["SecurityOpt"]:
                raise ValueError("Privilege escalation is not disabled")
            if "seccomp=" + self.seccomp not in config["SecurityOpt"]:
                raise ValueError("Seccomp profile changed")
            if item["Config"]["User"] != "65534:65534":
                raise ValueError("Container user changed")
            expected_environment = NATIVE_ENVIRONMENT if native_workspace else ENVIRONMENT
            if sorted(item["Config"]["Env"]) != sorted(f"{key}={value}" for key, value in expected_environment.items()):
                raise ValueError("Unexpected container environment")
            mounts = item["Mounts"]
            binds = [mount for mount in mounts if mount["Type"] == "bind"]
            if checkout is None:
                if binds:
                    raise ValueError("Unexpected host mount")
            elif len(binds) != 1 or binds[0]["Source"] != str(checkout) or \
                    binds[0]["Destination"] != ("/input" if native_workspace else "/workspace") or binds[0]["RW"] is not False:
                raise ValueError("Only the read-only disposable source may be mounted")
            workspace_output = "/workspace" if native_workspace else "/workspace/bin"
            temporary_paths = {"/tmp", workspace_output} if build_output or native_workspace else {"/tmp"}
            viewer_reports = build_output and self.task["id"] == "viewer-modal-v1"
            if viewer_reports:
                temporary_paths.add("/workspace/reports")
            if any(mount["Type"] != "bind" and
                   (mount["Type"] != "tmpfs" or mount["Destination"] not in temporary_paths) for mount in mounts):
                raise ValueError("Unexpected container mount")
            if build_output or native_workspace:
                outputs = [mount for mount in mounts if mount["Destination"] == workspace_output]
                temporary = config["Tmpfs"]
                options = set(temporary[workspace_output].split(","))
                required = {"rw", "exec", "nosuid", "nodev", "size=128m", "mode=1777"}
                if (set(temporary) != temporary_paths or not required <= options or
                        options - required - {"rprivate", "tmpcopyup"} or len(outputs) > 1 or
                        any(mount["Type"] != "tmpfs" or mount["RW"] is not True for mount in outputs)):
                    raise ValueError("Build output must use its own writable temporary filesystem")
            if viewer_reports:
                required = {"rw", "noexec", "nosuid", "nodev", "size=128m", "mode=1777"}
                options = set(config["Tmpfs"]["/workspace/reports"].split(","))
                if not required <= options or options - required - {"rprivate", "tmpcopyup"}:
                    raise ValueError("Viewer reports must use bounded non-executable temporary storage")
            if config["Devices"] or config["VolumesFrom"] or item.get("Pod"):
                raise ValueError("Unexpected device, shared volume or pod")
            if config["LogConfig"]["Type"] != "none":
                raise ValueError("Container log storage is not disabled")
        except (ValueError, KeyError, IndexError, TypeError, AttributeError) as error:
            raise Stop("blocked", f"Container policy could not be confirmed ({error}); refusing to start") from error

    def cleanup(self, name, container):
        previous = self.deadline, self.finalizing, self.cancel_requested
        self.deadline, self.finalizing, self.cancel_requested = time.monotonic() + 10, True, False
        try:
            check, _ = super().command(name + "-remove", PODMAN + ["rm", "--force", "--ignore", "--time=0", container["name"]], ROOT)
            if check["exit_code"] != 0:
                raise Stop("blocked", "Could not remove the owned container")
            check, _ = super().command(name + "-absent", PODMAN + ["container", "exists", container["name"]], ROOT)
            if check["exit_code"] != 1:
                raise Stop("blocked", "Container removal could not be confirmed")
            container["removed"] = True
            self.event("container_removed", container=container)
        finally:
            self.deadline, self.finalizing, self.cancel_requested = previous

    def checkout_path(self, cwd):
        checkout = Path(cwd).resolve()
        if checkout == ROOT or checkout.is_relative_to(ROOT) or not checkout.name.startswith("mapbox-calibration-"):
            raise Stop("invalid_input", "A disposable calibration copy outside the repository is required")
        return checkout

    def command(self, name, args, cwd, env_overrides=None):
        if args[0] != "go":
            allowed = {"podman-info": PODMAN + ["info", "--format=json"],
                       "snapshot": ["git", "archive", self.task["base_commit"]]}
            for stage in ("seed", "candidate"):
                path = str(self.path / f"{stage}.patch")
                allowed.update({stage + "-scope": ["git", "apply", "--numstat", "-z", path],
                                stage + "-metadata": ["git", "apply", "--summary", path],
                                stage + "-apply": ["git", "apply", "--whitespace=error", path]})
            if args != allowed.get(name) or env_overrides:
                raise Stop("invalid_input", "Host execution is limited to fixed inspection and patch commands")
            if name not in ("podman-info", "snapshot"):
                self.checkout_path(cwd)
            return super().command(name, args, cwd, env_overrides)
        if not self.prepared or env_overrides:
            raise Stop("blocked", "Container execution is not prepared for this check")
        version = name == "go-version" and args == ["go", "version"]
        if not version and (name not in ("baseline", "seed", "candidate") or args != self.test_command):
            raise Stop("invalid_input", "Only the fixed Go version and calibration checks are supported")
        return self.execute(name, args, cwd)

    def probe(self, name, cwd, arguments=()):
        if name not in ("boundary-probe", "timeout-probe", "cancel-probe"):
            raise Stop("invalid_input", "Unknown sandbox probe")
        return self.execute(name, ["/bin/sh", "/workspace/probe.sh", *arguments], cwd)

    def execute(self, name, args, cwd, build_output=False):
        if not self.prepared:
            raise Stop("blocked", "Container execution is not prepared")
        if build_output and (name != "candidate-suite" or args != repository_command(self.task)):
            raise Stop("invalid_input", "Temporary build output is only available to the fixed repository check")
        version = name == "go-version"
        checkout = None if version else self.checkout_path(cwd)
        if checkout is not None:
            checkout.chmod(0o755)
        container = {"name": "mapbox-harness-" + uuid.uuid4().hex, "check": name, "removed": False}
        self.result["containers"].append(container)
        create = PODMAN + ["--hooks-dir=" + str(self.hooks), "create", "--name=" + container["name"],
                           "--pull=never", "--network=none", "--read-only", "--read-only-tmpfs=false",
                           "--tmpfs=/tmp:rw,exec,nosuid,nodev,size=512m,mode=1777", "--image-volume=ignore",
                           "--cap-drop=all", "--security-opt=no-new-privileges", "--security-opt=seccomp=" + self.seccomp,
                           "--userns=nomap", "--user=65534:65534", "--pid=private", "--ipc=private", "--uts=private",
                           "--cgroupns=private", "--memory=1g", "--memory-swap=1g", "--cpu-period=100000",
                           "--cpu-quota=200000", "--pids-limit=256", "--http-proxy=false", "--unsetenv-all",
                           "--health-cmd=none", "--systemd=false", "--restart=no", "--log-driver=none",
                           "--timeout=" + str(max(1, math.ceil(self.deadline - time.monotonic()))),
                           "--workdir=" + ("/workspace" if checkout else "/tmp"),
                           "--entrypoint=" + {"go": "/usr/local/go/bin/go", "python3": "/usr/bin/python3",
                                              "make": "/usr/bin/make"}.get(args[0], "/bin/sh")]
        if checkout:
            create += ["--mount=type=bind,src=" + str(checkout) + ",dst=/workspace,ro"]
        if build_output:
            create += ["--tmpfs=/workspace/bin:rw,exec,nosuid,nodev,size=128m,mode=1777"]
            if self.task["id"] == "viewer-modal-v1":
                create += ["--tmpfs=/workspace/reports:rw,noexec,nosuid,nodev,size=128m,mode=1777"]
        for key, value in ENVIRONMENT.items():
            create += ["--env=" + key + "=" + value]
        try:
            check, output = super().command(name + "-create", create + [self.image, *args[1:]], ROOT)
            if check["exit_code"] != 0:
                raise Stop("blocked", "Could not create the restricted container")
            container["id"] = output.decode().strip()
            check, output = super().command(name + "-inspect", PODMAN + ["container", "inspect", container["name"]], ROOT)
            if check["exit_code"] != 0:
                raise Stop("blocked", "Could not inspect the created container")
            try:
                data = json.loads(output)
            except (ValueError, UnicodeDecodeError) as error:
                raise Stop("blocked", "Container inspection returned invalid JSON") from error
            self.validate_container(data, checkout, build_output=build_output)
            self.event("container_policy_checked", container=container)
            if name == "candidate":
                self.result["candidate_executed"] = None
            check, output = super().command(name, PODMAN + ["start", "--attach", container["name"]], ROOT,
                                            output_limit=OUTPUT_LIMIT)
            if version and (check["exit_code"] != 0 or not output.startswith(b"go version go1.27.1 ")):
                raise Stop("blocked", "The prepared image must provide Go v1.27.1")
            if check["exit_code"] in (125, 126, 127):
                raise Stop("blocked", "Container execution failed before the required check could be judged")
            state_check, state_output = super().command(name + "-status", PODMAN + ["container", "inspect", container["name"]], ROOT)
            try:
                state = json.loads(state_output)[0]["State"]
                if state_check["exit_code"] != 0 or state["Status"] != "exited" or state["ExitCode"] != check["exit_code"]:
                    raise ValueError("Container did not reach the observed exit state")
                container["state"] = state
                if state["OOMKilled"] is True:
                    raise Stop("blocked", "Container memory budget exhausted; repair quality was not established")
            except (ValueError, KeyError, IndexError, TypeError) as error:
                raise Stop("blocked", "Could not confirm the container's final state") from error
            if name == "candidate":
                self.result["candidate_executed"] = True
            if (self.task["id"] == "viewer-modal-v1" or self.result.get("case") == "viewer-modal-v1") and name != "go-version":
                artifacts = retain_viewer_artifacts(output, self.path / "captures" / name)
                check["viewer_artifacts"] = artifacts
                self.result.setdefault("viewer_artifacts", {})[name] = artifacts
            return check, output
        finally:
            self.cleanup(name, container)
