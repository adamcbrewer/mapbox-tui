import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import sandbox
from report import render
from run import HERE, Run, Stop


class SandboxTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.info = {
            "host": {"serviceIsRemote": False, "cgroupVersion": "v2",
                     "cgroupControllers": ["cpu", "memory", "pids"],
                     "security": {"rootless": True, "seccompEnabled": True,
                                  "seccompProfilePath": "/usr/share/containers/seccomp.json"}},
            "version": {"Version": "test-runtime"},
        }

    def test_runtime_description_rejects_missing_safety_prerequisites(self):
        cases = [("serviceIsRemote", True), ("cgroupVersion", "v1"),
                 ("cgroupControllers", ["cpu", "pids"]), ("cgroupControllers", "cpu memory pids")]
        for field, value in cases:
            with self.subTest(field=field, value=value):
                info = copy.deepcopy(self.info)
                info["host"][field] = value
                with self.assertRaises(Stop) as caught:
                    sandbox.runtime_details(info)
                self.assertEqual(caught.exception.outcome, "blocked")
                if field == "cgroupControllers" and isinstance(value, list):
                    self.assertIn("Missing cgroup controllers: memory", caught.exception.reason)
        for field in ("rootless", "seccompEnabled"):
            info = copy.deepcopy(self.info)
            info["host"]["security"][field] = False
            with self.subTest(field=field), self.assertRaises(Stop):
                sandbox.runtime_details(info)
        for info in ({}, [], None, {"host": None}, {**self.info, "version": {}}):
            with self.subTest(info=info), self.assertRaises(Stop):
                sandbox.runtime_details(info)

    def test_expected_description_only_checks_runtime_and_forces_local_inspection(self):
        run = Run(self.root)
        with patch("sandbox.os.geteuid", return_value=1000), patch("sandbox.sys.platform", "linux"), \
             patch.object(run, "command", return_value=({"exit_code": 0}, json.dumps(self.info).encode())) as command:
            sandbox.preflight(run)
        self.assertEqual(run.result["outcome"], "runtime_ready")
        self.assertEqual(run.result["runtime"]["podman_version"], "test-runtime")
        self.assertIn("not yet demonstrated", run.result["reason"])
        command.assert_called_once_with("podman-info", ["podman", "--remote=false", "info", "--format=json"], sandbox.ROOT)

    def test_runtime_description_requires_a_nonempty_seccomp_profile_string(self):
        for profile in (None, False, 0, 1, [], ["/profile"], {}, {"path": "/profile"}, "", " "):
            with self.subTest(profile=profile):
                info = copy.deepcopy(self.info)
                info["host"]["security"]["seccompProfilePath"] = profile
                with self.assertRaises(Stop) as caught:
                    sandbox.runtime_details(info)
                self.assertEqual(caught.exception.outcome, "blocked")
        del self.info["host"]["security"]["seccompProfilePath"]
        with self.assertRaises(Stop):
            sandbox.runtime_details(self.info)

    def test_malformed_seccomp_profile_blocks_both_image_entry_points_and_saves_receipt(self):
        import pi_run
        from container import ContainerRun

        self.info["host"]["security"]["seccompProfilePath"] = ["/profile"]
        for entry in (sandbox, pi_run):
            reports = self.root / entry.__name__
            with self.subTest(entry=entry.__name__), patch.object(entry, "REPORTS", reports), \
                    patch("sys.argv", [entry.__name__ + ".py", "--image", "sha256:" + "a" * 64, "--demo", "good"]), \
                    patch("sandbox.os.geteuid", return_value=1000), patch("sandbox.sys.platform", "linux"), \
                    patch.object(Run, "command", return_value=({"exit_code": 0}, json.dumps(self.info).encode())), \
                    patch.object(ContainerRun, "prepare") as prepare, patch("pi_run.pi_loop") as loop:
                self.assertEqual(entry.main(), 1)
            prepare.assert_not_called()
            loop.assert_not_called()
            receipt = next(reports.rglob("result.json"))
            result = json.loads(receipt.read_text())
            self.assertEqual(result["outcome"], "blocked")
            self.assertEqual(result["containers"], [])
            self.assertFalse(result.get("candidate_executed"))
            events = [json.loads(line) for line in receipt.with_name("events.jsonl").read_text().splitlines()]
            self.assertEqual(events[-1]["kind"], "run_finished")

    def test_failed_or_malformed_runtime_inspection_stops_without_other_commands(self):
        for check, output in (({"exit_code": 125}, b""), ({"exit_code": 0}, b"not JSON"),
                              ({"exit_code": 0}, b"null")):
            run = Run(self.root)
            with patch("sandbox.os.geteuid", return_value=1000), patch("sandbox.sys.platform", "linux"), \
                 patch.object(run, "command", return_value=(check, output)) as command:
                with self.assertRaises(Stop) as caught:
                    sandbox.preflight(run)
            self.assertEqual(caught.exception.outcome, "blocked")
            command.assert_called_once()

    def test_root_and_unsupported_platforms_never_start_podman(self):
        for platform, uid in (("linux", 0), ("darwin", 1000)):
            run = Run(self.root)
            with patch("sandbox.os.geteuid", return_value=uid), patch("sandbox.sys.platform", platform), \
                 patch.object(run, "command") as command:
                with self.assertRaises(Stop):
                    sandbox.preflight(run)
                command.assert_not_called()

    def test_missing_podman_saves_a_blocked_receipt_without_fallback(self):
        with patch("sandbox.REPORTS", self.root), patch("sys.argv", ["sandbox.py"]), \
             patch("sandbox.os.geteuid", return_value=1000), patch("sandbox.sys.platform", "linux"), \
             patch("run.subprocess.Popen", side_effect=FileNotFoundError("missing")) as launch:
            self.assertEqual(sandbox.main(), 1)
        launch.assert_called_once()
        self.assertEqual(launch.call_args.args[0], ["podman", "--remote=false", "info", "--format=json"])
        path = next(self.root.glob("sandbox-preflight/runs/*/result.json"))
        result = json.loads(path.read_text())
        self.assertEqual(result["outcome"], "blocked")
        self.assertFalse(result["candidate_executed"])
        self.assertFalse(result["isolation_demonstrated"])
        events = [json.loads(line) for line in path.with_name("events.jsonl").read_text().splitlines()]
        self.assertEqual(events[-1]["kind"], "run_finished")
        progress = json.loads((HERE / "progress.json").read_text())
        page = render(self.root, progress)
        self.assertIn("Missing executable: podman", page)
        self.assertIn("No candidate code or container was run", page)

    def test_successful_readiness_does_not_claim_candidate_execution_or_isolation(self):
        with patch("sandbox.REPORTS", self.root), patch("sys.argv", ["sandbox.py"]), \
             patch("sandbox.os.geteuid", return_value=1000), patch("sandbox.sys.platform", "linux"), \
             patch.object(Run, "command", return_value=({"exit_code": 0}, json.dumps(self.info).encode())):
            self.assertEqual(sandbox.main(), 0)
        result = json.loads(next(self.root.glob("sandbox-preflight/runs/*/result.json")).read_text())
        self.assertEqual(result["outcome"], "runtime_ready")
        self.assertFalse(result["candidate_executed"])
        self.assertFalse(result["isolation_demonstrated"])
        self.assertEqual(result["human_acceptance"], "pending")
        progress = json.loads((HERE / "progress.json").read_text())
        page = render(self.root, progress)
        self.assertIn("Container isolation still needs to be demonstrated", page)
        self.assertIn("No runs recorded", page)
        self.assertNotIn("The proposed repair passed this check.", page)

    def test_interrupted_inspection_keeps_its_outcome_and_restores_signal_handlers(self):
        import signal

        previous = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
        for outcome in ("timeout", "cancelled"):
            with self.subTest(outcome=outcome), patch("sandbox.REPORTS", self.root / outcome), \
                 patch("sys.argv", ["sandbox.py"]), patch("sandbox.preflight", side_effect=Stop(outcome, "Stopped")):
                self.assertEqual(sandbox.main(), 1)
            path = next((self.root / outcome).glob("sandbox-preflight/runs/*/result.json"))
            self.assertEqual(json.loads(path.read_text())["outcome"], outcome)
            for sig, handler in previous.items():
                self.assertEqual(signal.getsignal(sig), handler)


if __name__ == "__main__":
    unittest.main()
