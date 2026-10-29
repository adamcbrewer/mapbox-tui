import copy
import json
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import baseline
from run import Stop
from verify import fingerprint


class BaselineTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.context = {key: [] if key in ("tools", "activeTools") else {"id": "development"} if key == "task" else key
                        for key in baseline.CONTEXT}
        self.config = {"provider": "openai", "model": "gpt-6-luna", "auth": "subscription",
                       "maxModelRequests": 2, "maxOutputTokensPerRequest": None}
        self.plan = {"config": self.config, "context": self.context}
        self.run = SimpleNamespace(path=self.root, deadline=time.monotonic() + 30, event=Mock(),
                                   result={"dispatches": [], "provider_usage": []})
        self.tools = baseline.LiveTools(self.run, Mock(), self.plan)

    def ready(self):
        self.tools.check_context({**self.context, "permissions": {"network": True, "childProcess": False}})

    def request(self, number):
        return {"request": number, "provider": "openai", "model": "gpt-6-luna"}

    def test_context_must_match_before_any_model_reservation(self):
        with self.assertRaises(Stop):
            self.tools.reserve_model(self.request(1))
        for field in ("systemPrompt", "prompt", "tools", "activeTools", "task", "nodeVersion"):
            context = copy.deepcopy(self.context)
            context[field] = "changed"
            with self.subTest(field=field), self.assertRaises(Stop):
                self.tools.check_context({**context, "permissions": {"network": True, "childProcess": False}})
        with self.assertRaises(Stop):
            self.tools.check_context({**self.context, "permissions": {"network": True, "childProcess": True}})
        self.assertFalse((self.root / "reservations.json").exists())

    def test_reservations_are_saved_before_acknowledgement_and_never_exceed_budget(self):
        self.ready()
        for number in (1, 2):
            reply = self.tools.reserve_model(self.request(number))
            saved = json.loads((self.root / "reservations.json").read_text())
            self.assertEqual(len(saved), number)
            self.assertEqual(saved[-1]["request"], reply["request"])
        with self.assertRaises(Stop) as caught:
            self.tools.reserve_model(self.request(3))
        self.assertEqual(caught.exception.outcome, "budget_exhausted")
        self.assertEqual(len(json.loads((self.root / "reservations.json").read_text())), 2)

    def test_wrong_identity_sequence_and_elapsed_deadline_are_refused(self):
        self.ready()
        for request in (self.request(2), {**self.request(1), "model": "other"}, {**self.request(1), "request": True}):
            with self.subTest(request=request), self.assertRaises(Stop):
                self.tools.reserve_model(request)
        self.run.deadline = time.monotonic() - 1
        with self.assertRaises(Stop):
            self.tools.reserve_model(self.request(1))
        self.assertFalse((self.root / "reservations.json").exists())

    def test_unreserved_and_repeated_dispatch_events_are_refused(self):
        message = {"type": "model_dispatch", "request": 1}
        with self.assertRaises(Stop):
            self.tools.model_event(message)
        self.ready()
        self.tools.reserve_model(self.request(1))
        self.tools.model_event(message)
        with self.assertRaises(Stop):
            self.tools.model_event(message)
        self.assertEqual(self.run.result["dispatches"], [1])

    def test_plan_claim_is_one_use_even_after_an_interrupted_empty_claim(self):
        baseline.claim(self.root, "first")
        with self.assertRaises(Stop):
            baseline.claim(self.root, "second")
        self.assertEqual(json.loads((self.root / "attempt.json").read_text())["run_id"], "first")
        (self.root / "attempt.json").write_text("")
        with self.assertRaises(Stop):
            baseline.claim(self.root, "third")

    def test_plan_or_implementation_drift_stops_before_loading_task(self):
        plan = {"kind": "plain-pi-baseline-pilot", "sources": {"baseline.py": "frozen"}, "one_use": True,
                "config": self.config}
        saved = {"definition": plan, "sha256": fingerprint(plan)}
        (self.root / "plan.json").write_text(json.dumps(saved))
        with patch("baseline.sources", return_value={"baseline.py": "changed"}), patch("baseline.load_suite") as load, self.assertRaises(Stop):
            baseline.load_plan(self.root)
        load.assert_not_called()
        saved["definition"]["config"]["model"] = "changed"
        (self.root / "plan.json").write_text(json.dumps(saved))
        with patch("baseline.sources", return_value={"baseline.py": "frozen"}), self.assertRaises(Stop):
            baseline.load_plan(self.root)

    def test_reusing_plan_creates_no_model_or_container_work_and_still_reports(self):
        baseline.claim(self.root, "existing-run")
        (self.root / "plan.md").write_text("A frozen plan")
        plan = {"image": "sha256:" + "a" * 64, "config": self.config, "primary_metric": "fixed measure", "budgets": {"deadline_seconds": 1200}}
        with patch("baseline.REPORTS", self.root / "reports"), patch("baseline.load_plan", return_value=plan), \
                patch("baseline.NativeSandbox") as sandbox, patch("baseline.session") as session, \
                patch("baseline.token_summary", side_effect=ValueError("malformed usage")):
            path, result = baseline.execute(self.root)
        self.assertEqual(result["outcome"], "budget_exhausted")
        self.assertFalse(result["independent_checks_passed"])
        self.assertEqual(result["model_requests"], 0)
        self.assertEqual(result["containers"], [])
        self.assertIn("No model dispatch was observed", (path / "baseline.md").read_text())
        self.assertFalse(result["usage_summary"]["complete"])
        sandbox.assert_not_called()
        session.assert_not_called()


if __name__ == "__main__":
    unittest.main()
