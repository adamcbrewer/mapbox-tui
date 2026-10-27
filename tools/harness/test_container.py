import copy
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

import sandbox
from container import ContainerRun, ENVIRONMENT, MEMORY, NATIVE_ENVIRONMENT, OUTPUT_LIMIT
from report import render
from run import HERE, ROOT, Run, Stop


class ContainerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.checkout = self.root / "mapbox-calibration-test"
        self.checkout.mkdir()
        self.profile = self.root / "seccomp.json"
        self.profile.write_text("{}")
        self.image = "sha256:" + "a" * 64
        self.run = ContainerRun(self.root / "reports", self.image)
        self.run.seccomp = str(self.profile)
        self.run.prepared = True
        self.calls = []

    def description(self, checkout):
        return [{"Image": self.image, "BoundingCaps": [], "EffectiveCaps": [],
                 "State": {"Status": "exited", "ExitCode": 0, "OOMKilled": False},
                 "Config": {"User": "65534:65534", "Env": [f"{k}={v}" for k, v in ENVIRONMENT.items()]},
                 "HostConfig": {"NetworkMode": "none", "ReadonlyRootfs": True, "Privileged": False,
                                "PidMode": "private", "IpcMode": "private", "UsernsMode": "private", "Memory": MEMORY,
                                "CgroupMode": "private", "UTSMode": "private",
                                "Annotations": {"io.podman.annotations.userns": "nomap"},
                                "IDMappings": {"UidMap": ["0:1:65536"], "GidMap": ["0:1:65536"]},
                                "MemorySwap": MEMORY, "CpuPeriod": 100000, "CpuQuota": 200000,
                                "PidsLimit": 256, "Devices": [], "VolumesFrom": None, "LogConfig": {"Type": "none"},
                                "SecurityOpt": ["no-new-privileges", "seccomp=" + str(self.profile)]},
                 "Mounts": ([{"Type": "bind", "Source": str(checkout), "Destination": "/workspace", "RW": False}]
                            if checkout else [])}]

    def fake_command(self, run, name, args, cwd, env_overrides=None, output_limit=None):
        if name in ("baseline", "seed", "candidate", "go-version", "candidate-suite"):
            self.assertEqual(output_limit, OUTPUT_LIMIT)
        else:
            self.assertIsNone(output_limit)
        self.calls.append((name, args, run.finalizing, run.cancel_requested, run.deadline))
        output, code = b"", 0
        if name.endswith("-create"):
            output = ("b" * 64).encode()
        elif name.endswith(("-inspect", "-status")):
            output = json.dumps(self.description(None if name.startswith("go-version") else self.checkout)).encode()
        elif name.endswith("-absent"):
            code = 1
        elif name == "go-version":
            output = b"go version go1.27.1 linux/amd64\n"
        return {"id": name, "exit_code": code}, output

    def patched_command(self):
        return patch.object(Run, "command", lambda run, *args, **kwargs: self.fake_command(run, *args, **kwargs))

    def test_fixed_check_is_inspected_before_start_and_removed_afterwards(self):
        with self.patched_command():
            self.run.command("candidate", self.run.test_command, self.checkout)
        self.assertEqual([call[0] for call in self.calls],
                         ["candidate-create", "candidate-inspect", "candidate", "candidate-status", "candidate-remove", "candidate-absent"])
        create = self.calls[0][1]
        for flag in ("--pull=never", "--network=none", "--read-only", "--cap-drop=all", "--unsetenv-all",
                     "--pids-limit=256", "--memory=1g", "--cpu-quota=200000", "--userns=nomap", "--log-driver=none"):
            self.assertIn(flag, create)
        self.assertEqual(create[-len(self.run.test_command):], [self.image, *self.run.test_command[1:]])
        self.assertTrue(self.run.result["candidate_executed"])
        self.assertTrue(self.run.result["containers"][0]["removed"])

    def test_version_check_never_mounts_the_real_checkout(self):
        with self.patched_command():
            self.run.command("go-version", ["go", "version"], ROOT)
        self.assertFalse(any(arg.startswith("--mount") for arg in self.calls[0][1]))

    def test_repository_check_uses_only_temporary_build_output(self):
        args = ["make", "test", "BUILD_VCS=false", "COMMIT=" + self.run.task["base_commit"], "GOFLAGS=-mod=readonly -p=1 -trimpath"]
        description = self.description(self.checkout)
        description[0]["HostConfig"]["Tmpfs"] = {"/tmp": "rw,size=512m", "/workspace/bin": "rw,exec,nosuid,nodev,size=128m,mode=1777"}
        with self.patched_command(), patch.object(self, "description", return_value=description):
            self.run.execute("candidate-suite", args, self.checkout, build_output=True)
        create = self.calls[0][1]
        self.assertIn("--entrypoint=/usr/bin/make", create)
        self.assertIn("--tmpfs=/workspace/bin:rw,exec,nosuid,nodev,size=128m,mode=1777", create)
        self.assertIn("--mount=type=bind,src=" + str(self.checkout) + ",dst=/workspace,ro", create)
        self.assertTrue(self.run.result["containers"][0]["removed"])
        with self.assertRaises(Stop):
            self.run.execute("candidate-suite", ["make", "install"], self.checkout, build_output=True)

    def test_build_output_policy_requires_one_exact_temporary_mount(self):
        description = self.description(self.checkout)
        with self.assertRaises(Stop):
            self.run.validate_container(description, self.checkout, build_output=True)
        for mount in ({"Type": "tmpfs", "Destination": "/workspace/bin", "RW": False},
                      {"Type": "bind", "Source": "/tmp", "Destination": "/workspace/bin", "RW": True},
                      {"Type": "tmpfs", "Destination": "/workspace/scripts", "RW": True}):
            data = copy.deepcopy(description)
            data[0]["Mounts"].append(mount)
            with self.subTest(mount=mount), self.assertRaises(Stop):
                self.run.validate_container(data, self.checkout, build_output=True)
        description[0]["Mounts"].append({"Type": "tmpfs", "Destination": "/workspace/bin", "RW": True})
        with self.assertRaises(Stop):
            self.run.validate_container(description, self.checkout)
        description[0]["HostConfig"]["Tmpfs"] = {"/tmp": "rw,size=512m", "/workspace/bin": "rw,exec,nosuid,nodev,size=128m,mode=1777"}
        self.run.validate_container(description, self.checkout, build_output=True)
        for options in ("rw,exec,nosuid,nodev,size=1g,mode=1777", "ro,exec,nosuid,nodev,size=128m,mode=1777",
                        "rw,exec,suid,nodev,size=128m,mode=1777"):
            description[0]["HostConfig"]["Tmpfs"]["/workspace/bin"] = options
            with self.subTest(options=options), self.assertRaises(Stop):
                self.run.validate_container(description, self.checkout, build_output=True)

    def test_changed_container_policy_is_rejected(self):
        data = self.description(self.checkout)
        for key, value in (("NetworkMode", "host"), ("ReadonlyRootfs", False),
                           ("Memory", 0), ("PidsLimit", -1), ("Privileged", True),
                           ("SecurityOpt", ["seccomp=unconfined"]), ("LogConfig", {"Type": "journald"})):
            changed = copy.deepcopy(data)
            changed[0]["HostConfig"][key] = value
            with self.subTest(key=key), self.assertRaises(Stop):
                self.run.validate_container(changed, self.checkout)
        for field, value in (("Mounts", data[0]["Mounts"] + [{"Type": "bind", "Source": "/home", "RW": False}]),
                             ("Image", "sha256:" + "c" * 64), ("BoundingCaps", ["CAP_SYS_ADMIN"]), ("Pod", "shared")):
            changed = copy.deepcopy(data)
            changed[0][field] = value
            with self.subTest(field=field), self.assertRaises(Stop):
                self.run.validate_container(changed, self.checkout)
        changed = copy.deepcopy(data)
        changed[0]["Config"]["Env"].append("TOKEN=unexpected")
        with self.assertRaises(Stop):
            self.run.validate_container(changed, self.checkout)
        changed = copy.deepcopy(data)
        changed[0]["HostConfig"]["IDMappings"]["UidMap"] = ["0:0:65536"]
        with self.assertRaises(Stop):
            self.run.validate_container(changed, self.checkout)

    def test_native_workspace_keeps_host_source_read_only_and_uses_bounded_scratch(self):
        data = self.description(self.checkout)
        data[0]["Config"]["Env"] = [f"{k}={v}" for k, v in NATIVE_ENVIRONMENT.items()]
        data[0]["Mounts"][0]["Destination"] = "/input"
        data[0]["HostConfig"]["Tmpfs"] = {"/tmp": "rw,size=512m", "/workspace": "rw,exec,nosuid,nodev,size=128m,mode=1777"}
        self.run.validate_container(data, self.checkout, native_workspace=True)
        for change in ("writable-source", "wrong-source", "extra-bind", "oversized-workspace", "no-workspace", "network", "build-flags"):
            changed = copy.deepcopy(data)
            if change == "writable-source":
                changed[0]["Mounts"][0]["RW"] = True
            elif change == "wrong-source":
                changed[0]["Mounts"][0]["Destination"] = "/workspace"
            elif change == "extra-bind":
                changed[0]["Mounts"].append({"Type": "bind", "Source": "/home", "Destination": "/host", "RW": False})
            elif change == "oversized-workspace":
                changed[0]["HostConfig"]["Tmpfs"]["/workspace"] = "rw,exec,nosuid,nodev,size=1g,mode=1777"
            elif change == "no-workspace":
                del changed[0]["HostConfig"]["Tmpfs"]["/workspace"]
            elif change == "build-flags":
                changed[0]["Config"]["Env"] = [f"{k}={v}" for k, v in ENVIRONMENT.items()]
            else:
                changed[0]["HostConfig"]["NetworkMode"] = "host"
            with self.subTest(change=change), self.assertRaises(Stop):
                self.run.validate_container(changed, self.checkout, native_workspace=True)
        with self.assertRaises(Stop):
            self.run.validate_container(data, self.checkout, native_workspace=True, build_output=True)

    def test_podman_null_capabilities_are_empty_and_real_user_stays_unmapped(self):
        data = self.description(self.checkout)
        data[0]["BoundingCaps"] = None
        data[0]["EffectiveCaps"] = None
        self.run.validate_container(data, self.checkout)

    def test_rootless_mappings_use_intermediate_ids_and_exclude_zero(self):
        for kind in ("UidMap", "GidMap"):
            data = self.description(self.checkout)
            data[0]["HostConfig"]["IDMappings"][kind] = ["0:1000:1"]
            self.run.validate_container(data, self.checkout)
            for mapping in (["0:0:1"], ["0:1:10", "10:0:1"], ["0:-1:2"]):
                data[0]["HostConfig"]["IDMappings"][kind] = mapping
                with self.subTest(kind=kind, mapping=mapping), self.assertRaises(Stop):
                    self.run.validate_container(data, self.checkout)

    def test_missing_log_configuration_prevents_container_start(self):
        data = self.description(self.checkout)
        del data[0]["HostConfig"]["LogConfig"]
        with self.assertRaises(Stop):
            self.run.validate_container(data, self.checkout)

    def test_cgroup_and_uts_namespaces_must_be_confirmed_private(self):
        for field in ("CgroupMode", "UTSMode"):
            for value in ("host", "", None, [], "missing"):
                with self.subTest(field=field, value=value):
                    data = self.description(self.checkout)
                    if value == "missing":
                        del data[0]["HostConfig"][field]
                    else:
                        data[0]["HostConfig"][field] = value
                    with self.patched_command(), patch.object(self, "description", return_value=data), \
                            self.assertRaises(Stop):
                        self.run.command("baseline", self.run.test_command, self.checkout)
                    self.assertNotIn("baseline", [call[0] for call in self.calls])
                    self.assertTrue(self.run.result["containers"][-1]["removed"])

    def test_output_budget_exhaustion_still_removes_container(self):
        original = self.fake_command

        def excessive_output(run, name, *args, **kwargs):
            if name == "candidate":
                self.assertEqual(kwargs["output_limit"], OUTPUT_LIMIT)
                raise Stop("budget_exhausted", "Command output exceeded its limit")
            return original(run, name, *args, **kwargs)

        with patch.object(Run, "command", excessive_output), self.assertRaises(Stop) as caught:
            self.run.command("candidate", self.run.test_command, self.checkout)
        self.assertEqual(caught.exception.outcome, "budget_exhausted")
        self.assertTrue(self.run.result["containers"][-1]["removed"])
        self.assertIsNone(self.run.result["candidate_executed"])

    def test_failed_inspection_never_starts_and_still_removes_container(self):
        with self.patched_command(), patch.object(self.run, "validate_container", side_effect=Stop("blocked", "Unsafe")):
            with self.assertRaises(Stop):
                self.run.command("baseline", self.run.test_command, self.checkout)
        self.assertNotIn("baseline", [call[0] for call in self.calls])
        self.assertTrue(self.run.result["containers"][0]["removed"])

    def test_interrupted_create_and_start_still_receive_cleanup_time(self):
        original = self.fake_command
        for phase in ("candidate-create", "candidate"):
            for outcome in ("cancelled", "timeout"):
                with self.subTest(phase=phase, outcome=outcome):
                    self.calls.clear()
                    self.run.cancel_requested = False
                    self.run.deadline = time.monotonic() + 30

                    def interrupted(run, name, *args, **kwargs):
                        if name == phase:
                            run.cancel_requested = True
                            run.deadline = time.monotonic() - 1
                            raise Stop(outcome, "Interrupted")
                        return original(run, name, *args, **kwargs)

                    with patch.object(Run, "command", interrupted), self.assertRaises(Stop) as caught:
                        self.run.command("candidate", self.run.test_command, self.checkout)
                    self.assertEqual(caught.exception.outcome, outcome)
                    cleanup = [call for call in self.calls if call[0].endswith(("-remove", "-absent"))]
                    self.assertEqual(len(cleanup), 2)
                    self.assertTrue(all(call[2] and not call[3] and call[4] > time.monotonic() for call in cleanup))
                    self.assertTrue(self.run.result["containers"][-1]["removed"])
                    self.assertTrue(self.run.cancel_requested)

    def test_cleanup_failure_prevents_a_successful_result(self):
        original = self.fake_command

        def failed_cleanup(run, name, *args, **kwargs):
            if name.endswith("-absent"):
                return {"exit_code": 0}, b""
            return original(run, name, *args, **kwargs)

        with patch.object(Run, "command", failed_cleanup), self.assertRaises(Stop) as caught:
            self.run.command("candidate", self.run.test_command, self.checkout)
        self.assertEqual(caught.exception.outcome, "blocked")
        self.assertFalse(self.run.result["containers"][-1]["removed"])

    def test_arbitrary_commands_and_real_checkout_are_rejected_before_create(self):
        for name, args, cwd in (("candidate", ["go", "run", "untrusted.go"], self.checkout),
                                ("arbitrary", ["sh", "-c", "echo unsafe"], self.checkout),
                                ("candidate-apply", ["git", "apply", "--whitespace=error", str(self.run.path / "candidate.patch")], ROOT),
                                ("candidate", self.run.test_command, ROOT)):
            with self.subTest(args=args, cwd=cwd), self.patched_command(), self.assertRaises(Stop):
                self.run.command(name, args, cwd)
        self.assertEqual(self.calls, [])

    def test_out_of_memory_is_a_blocker_and_still_removes_the_container(self):
        original = self.fake_command

        def out_of_memory(run, name, *args, **kwargs):
            if name.endswith("-status"):
                data = self.description(self.checkout)
                data[0]["State"]["OOMKilled"] = True
                return {"exit_code": 0}, json.dumps(data).encode()
            return original(run, name, *args, **kwargs)

        with patch.object(Run, "command", out_of_memory), self.assertRaises(Stop) as caught:
            self.run.command("candidate", self.run.test_command, self.checkout)
        self.assertEqual(caught.exception.outcome, "blocked")
        self.assertIn("memory budget", caught.exception.reason)
        self.assertTrue(self.run.result["containers"][-1]["removed"])

    def test_mutable_images_are_rejected_without_inspection(self):
        self.run.image = "golang:latest"
        with self.patched_command(), self.assertRaises(Stop) as caught:
            self.run.prepare()
        self.assertEqual(caught.exception.outcome, "invalid_input")
        self.assertEqual(self.calls, [])

    def test_image_preparation_requires_exact_local_identity_and_seccomp_profile(self):
        self.run.result["runtime"] = {"seccomp_profile": str(self.profile)}
        for identity in (self.image, "sha256:" + "c" * 64):
            with self.subTest(identity=identity), patch.object(Run, "command", return_value=(
                    {"exit_code": 0}, json.dumps([{"Id": identity, "RepoDigests": []}]).encode())):
                if identity == self.image:
                    self.run.prepare()
                    self.assertIn("seccomp_sha256", self.run.result)
                else:
                    with self.assertRaises(Stop):
                        self.run.prepare()
        self.profile.unlink()
        with self.patched_command(), self.assertRaises(Stop):
            self.run.prepare()
        self.assertEqual(self.calls, [])

    def test_container_cli_does_not_fall_back_to_host_when_podman_is_missing(self):
        with patch("sandbox.REPORTS", self.root / "cli"), patch("sys.argv", ["sandbox.py", "--image", self.image]), \
             patch("sandbox.os.geteuid", return_value=1000), patch("sandbox.sys.platform", "linux"), \
             patch("run.subprocess.Popen", side_effect=FileNotFoundError("missing")) as launch:
            self.assertEqual(sandbox.main(), 1)
        launch.assert_called_once()
        self.assertEqual(launch.call_args.args[0], ["podman", "--remote=false", "info", "--format=json"])
        result = json.loads(next((self.root / "cli").glob("runs/*/result.json")).read_text())
        self.assertEqual(result["outcome"], "blocked")
        self.assertEqual(result["execution_mode"], "rootless-podman")
        self.assertEqual(result["containers"], [])
        self.assertFalse(result["candidate_executed"])
        page = render(self.root / "cli", dict(json.loads((HERE / "progress.json").read_text()), stage="Connect"))
        self.assertIn("requested rootless Podman", page)
        self.assertIn("Removal confirmed: 0", page)
        result["containers"] = None
        path = next((self.root / "cli").glob("runs/*/result.json"))
        path.write_text(json.dumps(result))
        page = render(self.root / "cli", dict(json.loads((HERE / "progress.json").read_text()), stage="Connect"))
        self.assertIn("Container cleanup records are unavailable", page)
        self.assertNotIn("Removal confirmed: 0", page)


if __name__ == "__main__":
    unittest.main()
