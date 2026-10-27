import base64
import difflib
import json
import math
from pathlib import Path
import time
import uuid

from container import ContainerRun, NATIVE_ENVIRONMENT, OUTPUT_LIMIT, PODMAN
from run import HERE, ROOT, Run, Stop, digest, write_json


class NativeRun(ContainerRun):
    def event(self, kind, **fields):
        super().event(kind, **fields)
        write_json(self.path / "result.json", self.result)


class NativeSandbox:
    def __init__(self, run, checkout):
        self.run = run
        self.checkout = run.checkout_path(checkout)
        self.container = {"name": "mapbox-harness-" + uuid.uuid4().hex, "check": "native-tools", "removed": False}
        run.result["containers"].append(self.container)
        self.label = f"native-{len(run.result['containers'])}"
        self.operations = 0
        self.operation_limit = max(100, 10 * run.result.get("budgets", {}).get("tool_calls", 0))
        run.result["native_environment"] = NATIVE_ENVIRONMENT.copy()
        self.worker = (HERE / "native_worker.py").read_text()
        self.seeded = (self.checkout / "go/import.go").read_text()
        self.expected = {str(path.relative_to(self.checkout)): digest(path.read_bytes())
                         for path in self.checkout.rglob("*") if path.is_file()}

    def host(self, name, args):
        name = self.label + "-" + name.removeprefix("native-")
        check, output = Run.command(self.run, name, args, ROOT, output_limit=OUTPUT_LIMIT)
        if check["exit_code"] != 0:
            raise Stop("blocked", "I couldn't complete the sandbox operation; inspect its log")
        return output

    def start(self):
        run = self.run
        self.checkout.chmod(0o755)
        create = PODMAN + ["--hooks-dir=" + str(run.hooks), "create", "--name=" + self.container["name"],
                           "--pull=never", "--network=none", "--read-only", "--read-only-tmpfs=false",
                           "--tmpfs=/tmp:rw,exec,nosuid,nodev,size=512m,mode=1777",
                           "--tmpfs=/workspace:rw,exec,nosuid,nodev,size=128m,mode=1777", "--image-volume=ignore",
                           "--cap-drop=all", "--security-opt=no-new-privileges", "--security-opt=seccomp=" + run.seccomp,
                           "--userns=nomap", "--user=65534:65534", "--pid=private", "--ipc=private", "--uts=private",
                           "--cgroupns=private", "--memory=1g", "--memory-swap=1g", "--cpu-period=100000",
                           "--cpu-quota=200000", "--pids-limit=256", "--http-proxy=false", "--unsetenv-all",
                           "--health-cmd=none", "--systemd=false", "--restart=no", "--log-driver=none",
                           "--timeout=" + str(max(1, math.ceil(run.deadline - time.monotonic()))),
                           "--mount=type=bind,src=" + str(self.checkout) + ",dst=/input,ro",
                           "--workdir=/workspace", "--entrypoint=/usr/bin/python3"]
        for key, value in NATIVE_ENVIRONMENT.items():
            create += ["--env=" + key + "=" + value]
        bootstrap = ('import shutil,time; from pathlib import Path\n'
                     'try:\n'
                     ' for source in Path("/input").iterdir():\n'
                     '  destination=Path("/workspace")/source.name\n'
                     '  shutil.copytree(source,destination) if source.is_dir() else shutil.copy2(source,destination)\n'
                     ' Path("/tmp/native-ready").touch()\n'
                     'except Exception as error: Path("/tmp/native-error").write_text(str(error))\n'
                     'time.sleep(600)')
        self.container["id"] = self.host("native-create", create + [run.image, "-I", "-u", "-c", bootstrap]).decode().strip()
        data = json.loads(self.host("native-inspect", PODMAN + ["container", "inspect", self.container["name"]]))
        run.validate_container(data, self.checkout, native_workspace=True)
        run.event("container_policy_checked", container=self.container)
        self.host("native-start", PODMAN + ["start", self.container["name"]])
        ready = ('import time; from pathlib import Path; deadline=time.monotonic()+5\n'
                 'while not Path("/tmp/native-ready").exists():\n'
                 ' if Path("/tmp/native-error").exists(): raise RuntimeError(Path("/tmp/native-error").read_text())\n'
                 ' if time.monotonic()>deadline: raise TimeoutError("Workspace did not start")\n'
                 ' time.sleep(0.01)')
        try:
            self.host("native-ready", PODMAN + ["exec", self.container["name"], "/usr/bin/python3", "-I", "-c", ready])
        except Stop:
            self.host("native-status", PODMAN + ["container", "inspect", self.container["name"]])
            raise

    def request(self, request):
        self.operations += 1
        if self.operations > self.operation_limit:
            raise Stop("budget_exhausted", "The sandbox operation limit was reached")
        payload = json.dumps(request)
        if len(payload.encode()) > 64 * 1024:
            return {"ok": False, "error": "The operation exceeds the 64 KiB request limit"}
        raw = self.host(f"native-op-{self.operations}", PODMAN + ["exec", self.container["name"],
                                                               "/usr/bin/python3", "-I", "-c", self.worker, payload])
        result = json.loads(raw)
        if not isinstance(result, dict) or type(result.get("ok")) is not bool:
            raise Stop("blocked", "The sandbox returned an unreadable response")
        return result

    def capture(self):
        self.run.result.update(snapshot_scope_valid=False, patch_sha256=None)
        response = self.request({"op": "snapshot"})
        if not response["ok"]:
            raise Stop("incomplete", "I couldn't recover the current project files")
        snapshot = response["value"]
        write_json(self.run.path / "project-snapshot.json", snapshot)
        source = snapshot.get("source")
        if source is None:
            raise Stop("invalid_input", "The allowed source file is missing or linked")
        raw = base64.b64decode(source, validate=True)
        (self.run.path / "candidate-source.bin").write_bytes(raw)
        changed = {name for name in self.expected.keys() | snapshot["files"].keys()
                   if self.expected.get(name) != snapshot["files"].get(name)}
        self.run.result["changed_files"] = sorted(changed)
        patch = "".join(difflib.unified_diff(self.seeded.splitlines(True), raw.decode().splitlines(True),
                                           fromfile="a/go/import.go", tofile="b/go/import.go")).encode()
        (self.run.path / "candidate.patch").write_bytes(patch)
        self.run.result.update(patch_sha256=digest(patch), snapshot_scope_valid=not snapshot["invalid"] and
                               not changed - {"go/import.go"}, snapshot_source_sha256=digest(raw))
        self.run.result.pop("snapshot_problem", None)
        self.run.event("native_snapshot_captured", changed_files=sorted(changed))
        return snapshot

    def close(self):
        previous = self.run.deadline, self.run.finalizing, self.run.cancel_requested
        self.run.deadline, self.run.finalizing, self.run.cancel_requested = time.monotonic() + 10, True, False
        try:
            try:
                self.capture()
            except (Stop, OSError, ValueError, KeyError, TypeError) as error:
                self.run.result["snapshot_problem"] = error.reason if isinstance(error, Stop) else str(error)
            finally:
                self.run.cleanup(self.label, self.container)
        finally:
            self.run.deadline, self.run.finalizing, self.run.cancel_requested = previous
