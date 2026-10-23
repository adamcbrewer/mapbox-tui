import json
import unittest

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


if __name__ == "__main__":
    unittest.main()
