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
