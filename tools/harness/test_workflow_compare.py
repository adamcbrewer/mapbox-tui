import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from run import Stop
from verify import fingerprint
import workflow_compare as comparison


class WorkflowComparisonTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.path = Path(temporary.name) / "experiment"
        self.path.mkdir()
        self.common = {"kind": "native-repair-workflow-v1", "mode": "plain", "config": {"maxModelRequests": 10},
                       "case": {"id": "task", "partition": "development"}, "checkpoint_limit": 4,
                       "budgets": {"verification_attempts": 2, "tool_calls": 30}, "sources": comparison.workflow.sources()}
        with patch("workflow_compare.workflow.prepare", return_value=self.path), \
                patch("workflow_compare.workflow.load_plan", return_value=copy.deepcopy(self.common)):
            comparison.prepare(Path("suite"), "image", Path("config"))

    def test_only_workflow_mode_differs_between_the_frozen_pairs(self):
        plan = comparison.load_plan(self.path, execution=False)
        self.assertEqual(plan["maximum_model_requests"], 40)
        self.assertEqual([(c["pair"], c["mode"]) for c in plan["cells"]], list(comparison.ORDER))
        for cell in plan["cells"]:
            child = comparison.workflow.load_plan(self.path / "cells" / str(cell["position"]), execution=False)
            child.pop("comparison")
            child["mode"] = "plain"
            self.assertEqual(child, self.common)

    def test_code_drift_or_a_spent_experiment_sends_no_model_work(self):
        with patch("workflow_compare.sources", return_value={}), patch("workflow_compare.workflow.execute") as execute:
            with self.assertRaises(Stop):
                comparison.execute(self.path)
        execute.assert_not_called()
        comparison.baseline.claim(self.path, "spent")
        with patch("workflow_compare.load_plan", return_value={"cells": []}), patch("workflow_compare.workflow.execute") as execute:
            with self.assertRaises(Stop):
                comparison.execute(self.path)
        execute.assert_not_called()

    def test_extra_cell_change_is_refused_even_if_its_hash_was_recomputed(self):
        p = self.path / "plan.json"
        saved = json.loads(p.read_text())
        cell_path = self.path / "cells/1/plan.json"
        cell = json.loads(cell_path.read_text())
        cell["definition"]["config"]["maxModelRequests"] = 11
        cell["sha256"] = fingerprint(cell["definition"])
        cell_path.write_text(json.dumps(cell))
        saved["definition"]["cells"][0]["plan_sha256"] = cell["sha256"]
        saved["sha256"] = fingerprint(saved["definition"])
        p.write_text(json.dumps(saved))
        with self.assertRaises(Stop):
            comparison.load_plan(self.path, execution=False)

    def test_blocked_conversation_stops_later_cells_even_if_checking_reports_another_failure(self):
        cases = (("timeout", "blocked", "blocked"), ("incomplete", "cancelled", "cancelled"),
                 ("blocked", "finished", "blocked"), ("repair_failed", "finished", None))
        for index, (outcome, agent, stop) in enumerate(cases):
            with self.subTest(outcome=outcome, agent=agent):
                experiment = self.path / str(index)
                experiment.mkdir()
                directory = experiment / "run"
                directory.mkdir()
                result = {"outcome": outcome, "agent_outcome": agent}
                (directory / "result.json").write_text(json.dumps(result))
                plan = {"cells": [{"position": n, "mode": "plain"} for n in (1, 2)]}
                with patch("workflow_compare.load_plan", return_value=plan), \
                        patch("workflow_compare.workflow.execute", return_value=(directory, result)) as execute, \
                        patch("workflow_compare.summarize"):
                    if stop:
                        with self.assertRaises(Stop) as caught:
                            comparison.execute(experiment)
                        self.assertEqual(caught.exception.outcome, stop)
                        self.assertEqual(execute.call_count, 1)
                    else:
                        comparison.execute(experiment)
                        self.assertEqual(execute.call_count, 2)

    def test_tie_keeps_the_simple_workflow_and_bad_claims_block_adoption(self):
        plan = comparison.load_plan(self.path, execution=False)
        rows = [{**c, "status": "finished", "run_id": str(c["position"]), "receipt_sha256": "receipt",
                 "verified": True, "without_operator_correction": True, "first_candidate_passed": True,
                 "attempts": 1, "operator_corrections_delivered": 0, "requests": 2, "tools": 3,
                 "duration_seconds": 4, "usage": {"complete": False}} for c in plan["cells"]]
        review = {"runs": [{"run_id": r["run_id"], "receipt_sha256": "receipt", "incorrect_success_claim": False} for r in rows]}
        review_path = self.path / "claim-review.json"
        review_path.write_text(json.dumps(review))
        with patch("workflow_compare.collect", side_effect=rows):
            result = comparison.summarize(self.path)
        self.assertFalse(result["primary_improved"])
        self.assertTrue(result["claim_review_complete"])
        self.assertIn("Keep plain Pi", result["recommendation"])
        self.assertIsNone(result["totals"]["plain"]["tokens"])
        review["runs"][1]["incorrect_success_claim"] = True
        review_path.write_text(json.dumps(review))
        with patch("workflow_compare.collect", side_effect=rows):
            result = comparison.summarize(self.path)
        self.assertTrue(result["incorrect_success_claim_found"])
        self.assertIn("Don't adopt", result["recommendation"])


if __name__ == "__main__":
    unittest.main()
