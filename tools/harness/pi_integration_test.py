import json
import base64
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

from native_run import NativeTools, session
from native_sandbox import NativeRun
from pi_run import pi_loop
from repair import repair_receipt_matches
from test_repair import RepairFixture
from timeout_recovery import MESSAGES, RecoveryTools, supply_failure


class PiIntegrationTests(RepairFixture, unittest.TestCase):
    def test_actual_pi_session_receives_failed_verification_before_second_edit(self):
        pi_loop(self.run, self.tools, self.root, "retry")
        self.tools.finish()
        session = json.loads((self.run.path / "pi-session.json").read_text())
        feedback = [json.loads(part["text"]) for message in session["messages"] if message["role"] == "toolResult"
                    for part in message["content"] if part["type"] == "text"]
        self.assertEqual([r["remaining_attempts"] for r in feedback if "remaining_attempts" in r], [1, 0])
        self.assertTrue(repair_receipt_matches(self.run.result, self.run.path, self.root))


class NativeTimeoutIntegrationTests(unittest.TestCase):
    def test_controller_feedback_crosses_python_and_actual_pi_without_resetting_tools(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            run = NativeRun(root / "reports", "sha256:" + "a" * 64)
            sandbox = Mock()
            sandbox.request.return_value = {"ok": True, "value": {"exitCode": 0, "output": base64.b64encode(b"/workspace\n").decode()}}
            tools = NativeTools(run, sandbox)
            tools.checkpoint = Mock(side_effect=[{"action": "continue", "prompt": "A supplied check failed; try the next step."}, {"action": "stop"}])
            session(run, tools, {"workflowController": True, "task": {"id": "controller"}, "calls": [
                {"name": "bash", "args": {"command": "pwd"}}, None,
                {"name": "bash", "args": {"command": "pwd"}}, None]}, root)
            self.assertEqual(tools.calls, 2)
            self.assertEqual(tools.checkpoint.call_count, 2)
            messages = json.loads((run.path / "pi-session.json").read_text())["messages"]
            self.assertEqual(sum(m["role"] == "toolResult" for m in messages), 2)
            self.assertIn("supplied check failed", json.dumps(messages))
            self.assertTrue((run.path / "live-messages.jsonl").is_file())

    def test_both_comparison_fixtures_use_actual_pi_without_executing_invalid_call(self):
        for version, message in MESSAGES.items():
            with self.subTest(version=version), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                run = NativeRun(root / "reports", "sha256:" + "a" * 64)
                run.result.update(model_requests=0, dispatches=[])
                sandbox = Mock()
                tools = RecoveryTools(run, sandbox, {"feedback": version})
                supply_failure(run, tools, root, version)
                sandbox.request.assert_not_called()
                self.assertEqual(tools.calls, 1)
                self.assertEqual(run.result["model_requests"], 0)
                self.assertEqual(tools.results, [{"name": "bash", "error": message}])
                saved = json.loads((run.path / "supplied-pi-session.json").read_text())
                errors = [m for m in saved["messages"] if m["role"] == "toolResult"]
                self.assertEqual(len(errors), 1)
                self.assertTrue(errors[0]["isError"])
                self.assertIn(message, errors[0]["content"][0]["text"])

    def test_refused_command_does_not_execute_and_corrected_call_uses_same_budget(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            run = NativeRun(root / "reports", "sha256:" + "a" * 64)
            sandbox = Mock()
            sandbox.request.return_value = {"ok": True, "value": {"exitCode": 0, "output": base64.b64encode(b"accepted\n").decode()}}
            tools = NativeTools(run, sandbox)
            calls = [{"name": "bash", "args": {"command": "must not execute", "timeout": 120000}},
                     {"name": "bash", "args": {"command": "corrected request", "timeout": 120}}]
            session(run, tools, {"task": {"id": "timeout-feedback"}, "calls": calls}, root)
            sandbox.request.assert_called_once_with({"op": "bash", "command": "corrected request", "timeout": 120})
            self.assertEqual(tools.calls, 2)
            self.assertIsNotNone(tools.results[0]["error"])
            self.assertIsNone(tools.results[1]["error"])
            messages = json.loads((run.path / "pi-session.json").read_text())["messages"]
            results = [message for message in messages if message["role"] == "toolResult"]
            refusal = results[0]["content"][0]["text"]
            self.assertIn("in seconds", refusal)
            self.assertIn("at most 600", refusal)
            self.assertIn("two minutes, use 120", refusal)
            self.assertTrue(results[0]["isError"])
            self.assertNotEqual(results[1].get("isError"), True)


if __name__ == "__main__":
    unittest.main()
