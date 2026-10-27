import base64
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import native_worker as worker
import operator_log
from workflow import WorkflowTools
from native_run import NativeTools, ToolCallLimit
from native_sandbox import NativeRun, NativeSandbox
from run import Stop, digest


class WorkerTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        patched = patch.object(worker, "ROOT", self.root)
        patched.start()
        self.addCleanup(patched.stop)

    def test_read_write_and_access_use_project_files(self):
        worker.dispatch({"op": "mkdir", "path": "go"})
        worker.dispatch({"op": "write", "path": "go/import.go", "content": "hello\n"})
        worker.dispatch({"op": "access", "path": "go/import.go", "write": True})
        result = worker.dispatch({"op": "read", "path": "go/import.go"})
        self.assertEqual(base64.b64decode(result["data"]), b"hello\n")

    def test_relative_absolute_and_symlink_escapes_are_rejected(self):
        (self.root / "escape").symlink_to(self.root.parent, target_is_directory=True)
        for path in ("../host", "/etc/passwd", "escape/host", "bad\0path", ""):
            for operation in ("read", "write", "access", "mkdir"):
                with self.subTest(path=path, op=operation), self.assertRaises(ValueError):
                    worker.dispatch({"op": operation, "path": path, "content": "no"})

    def test_read_and_write_limits_are_enforced(self):
        (self.root / "large").write_bytes(b"x" * (worker.LIMIT + 1))
        with self.assertRaises(ValueError):
            worker.dispatch({"op": "read", "path": "large"})
        with self.assertRaises(ValueError):
            worker.dispatch({"op": "write", "path": "new", "content": "x" * (256 * 1024 + 1)})
        self.assertFalse((self.root / "new").exists())

    def test_snapshot_hashes_large_fixture_and_flags_links(self):
        (self.root / "go").mkdir()
        (self.root / "go/import.go").write_text("source")
        fixture = b"x" * (3 * worker.LIMIT)
        (self.root / "fixture").write_bytes(fixture)
        (self.root / "linked").symlink_to("go/import.go")
        os.mkfifo(self.root / "pipe")
        result = worker.snapshot()
        self.assertEqual(result["files"]["fixture"], digest(fixture))
        self.assertEqual(base64.b64decode(result["source"]), b"source")
        self.assertEqual(result["invalid"], ["linked", "pipe"])

    def test_bash_returns_combined_output_and_nonzero_exit(self):
        result = worker.bash("printf hello; printf error >&2; exit 7", 2)
        self.assertEqual(base64.b64decode(result["output"]), b"helloerror")
        self.assertEqual(result["exitCode"], 7)

    def test_bash_timeout_and_output_limit_stop_commands(self):
        with self.assertRaises(TimeoutError):
            worker.bash("sleep 5", 0.05)
        with patch.object(worker, "LIMIT", 10), self.assertRaises(ValueError):
            worker.bash("printf 123456789012345", 2)


class NativeProtocolTests(unittest.TestCase):
    def setUp(self):
        self.run = SimpleNamespace(event=Mock(), deadline=time.monotonic() + 30, result={})
        self.sandbox = SimpleNamespace(capture=Mock(), request=Mock(return_value={"ok": True, "value": {}}))
        self.tools = NativeTools(self.run, self.sandbox)

    def start(self, name="read", identity="one"):
        self.tools.dispatch({"type": "native_tool", "toolCallId": identity, "name": name})

    def test_saved_thirty_call_budget_stops_before_executing_call_thirty_one(self):
        self.run.result["budgets"] = {"tool_calls": 30}
        self.tools = NativeTools(self.run, self.sandbox)
        for number in range(30):
            self.start(identity=str(number))
            self.tools.dispatch({"type": "native_tool_finished", "toolCallId": str(number)})
        with self.assertRaises(ToolCallLimit):
            self.start(identity="too-many")
        self.assertEqual(self.tools.calls, 30)
        self.assertIsNone(self.tools.active)

    def test_operations_require_the_right_active_tool_and_identity(self):
        for request in ({"type": "native_fs", "toolCallId": "one", "op": "read"},
                        {"type": "native_tool_finished", "toolCallId": "one"}):
            with self.assertRaises(Stop):
                self.tools.dispatch(request)
        self.start()
        for identity, operation in (("other", "read"), ("one", "bash"), ("one", "write"), ("one", "snapshot")):
            with self.subTest(identity=identity, operation=operation), self.assertRaises(Stop):
                self.tools.dispatch({"type": "native_fs", "toolCallId": identity, "op": operation})
        self.sandbox.request.assert_not_called()

    def test_failed_tools_are_recorded_and_snapshot_failure_propagates(self):
        self.start()
        self.sandbox.capture.side_effect = Stop("incomplete", "Snapshot unavailable")
        with self.assertRaises(Stop):
            self.tools.dispatch({"type": "native_tool_finished", "toolCallId": "one", "error": "Refused"})
        self.assertEqual(self.tools.results, [{"name": "read", "error": "Refused"}])
        self.assertIsNone(self.tools.active)

    def test_duplicate_ids_and_call_limit_fail_closed(self):
        self.start()
        self.tools.dispatch({"type": "native_tool_finished", "toolCallId": "one"})
        with self.assertRaises(Stop):
            self.start()
        self.tools.calls = 12
        with self.assertRaises(Stop) as caught:
            self.start(identity="new")
        self.assertEqual(caught.exception.outcome, "budget_exhausted")

    def test_bash_timeout_is_clamped_to_remaining_run_time(self):
        self.start("bash")
        self.tools.dispatch({"type": "native_fs", "toolCallId": "one", "op": "bash", "command": "go test", "timeout": 600})
        self.assertLess(self.sandbox.request.call_args.args[0]["timeout"], 30)
        self.assertEqual(self.tools.dispatch({"type": "native_fs", "toolCallId": "one", "op": "bash", "timeout": True})["ok"], False)


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="mapbox-calibration-native-test-")
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        checkout = root / "mapbox-calibration-project"
        (checkout / "go").mkdir(parents=True)
        (checkout / "go/import.go").write_text("old\n")
        (checkout / "task.json").write_text("task")
        self.run = NativeRun(root / "reports", "sha256:" + "a" * 64)
        self.sandbox = NativeSandbox(self.run, checkout)
        self.snapshot = {"files": {"go/import.go": digest(b"new\n"), "task.json": digest(b"task")},
                         "source": base64.b64encode(b"new\n").decode(), "invalid": []}

    def test_retry_preserves_setup_operation_and_cleanup_logs_from_both_sandboxes(self):
        suite = self.sandbox.checkout.parent / "suite"
        shutil.copytree(self.sandbox.checkout, suite / "cases/task/agent")
        plan = {"suite": str(suite), "case": {"id": "task", "agent_files_sha256": self.sandbox.expected}}
        tools = WorkflowTools(self.run, self.sandbox, plan, self.sandbox.checkout)
        operator_log.create(self.run.path)
        (self.run.path / "candidate-source.bin").write_bytes(b"new\n")
        self.run.seccomp, self.run.hooks = "seccomp", Path("hooks")
        script = ("import json,sys; print(json.dumps({'ok': True, 'value': {'args': sys.argv[1:]}})); "
                  "print(sys.argv[1:], file=sys.stderr); sys.exit(1 if 'exists' in sys.argv[1:] else 0)")
        fake_podman = [sys.executable, "-I", "-c", script]
        with patch("native_sandbox.PODMAN", fake_podman), patch("container.PODMAN", fake_podman), \
                patch.object(self.run, "validate_container"), patch.object(NativeSandbox, "capture"):
            self.sandbox.start()
            self.sandbox.request({"op": "read", "path": "task.json"})
            tools.close()
            original = {check[stream]: (self.run.path / check[stream]).read_bytes()
                        for check in self.run.result["checks"] for stream in ("stdout", "stderr")}
            tools.reopen()
            tools.sandbox.request({"op": "read", "path": "go/import.go"})
            tools.close()
        checks = self.run.result["checks"]
        self.assertEqual(len(checks), 14)
        self.assertEqual(len({check["id"] for check in checks}), len(checks))
        for name, raw in original.items():
            self.assertEqual((self.run.path / name).read_bytes(), raw)

    def test_source_only_patch_is_recovered_and_test_changes_flagged(self):
        with patch.object(self.sandbox, "request", return_value={"ok": True, "value": self.snapshot}):
            self.sandbox.capture()
            self.assertTrue(self.run.result["snapshot_scope_valid"])
            self.assertIn("+new", (self.run.path / "candidate.patch").read_text())
            self.snapshot["files"]["task.json"] = digest(b"changed test")
            self.sandbox.capture()
            self.assertFalse(self.run.result["snapshot_scope_valid"])

    def test_failed_snapshot_cannot_reuse_an_earlier_pass(self):
        with patch.object(self.sandbox, "request", return_value={"ok": True, "value": self.snapshot}):
            self.sandbox.capture()
        with patch.object(self.sandbox, "request", return_value={"ok": False}), self.assertRaises(Stop):
            self.sandbox.capture()
        self.assertFalse(self.run.result["snapshot_scope_valid"])
        self.assertIsNone(self.run.result["patch_sha256"])

    def test_isolated_python_ignores_project_module_replacements(self):
        calls = []
        self.run.seccomp, self.run.hooks = "seccomp", Path("hooks")

        def host(name, args):
            calls.append((name, args))
            return b'[{"State": {}}]' if name == "native-inspect" else b'{}'

        with patch.object(self.sandbox, "host", side_effect=host), patch.object(self.run, "validate_container"):
            self.sandbox.start()
        self.assertIn("-I", calls[0][1])
        self.assertIn("-I", calls[-1][1])
        with patch.object(self.sandbox, "host", return_value=b'{"ok":true,"value":{}}') as command:
            self.sandbox.request({"op": "snapshot"})
        self.assertIn("-I", command.call_args.args[1])


if __name__ == "__main__":
    unittest.main()
