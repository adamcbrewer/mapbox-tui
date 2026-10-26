import json
from pathlib import Path
import unittest
from unittest.mock import patch

import compare
from recovery import durable_json
from run import Stop, digest
from test_repair import RepairFixture


class ComparisonTests(RepairFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        for module in ("compare", "recover", "recovery"):
            self.enterContext(patch(module + ".REPORTS", self.root))
        self.enterContext(patch("compare.preflight", side_effect=self.preflight))
        self.enterContext(patch("recover.preflight", side_effect=self.preflight))
        self.loop = self.enterContext(patch("recover.pi_loop", side_effect=AssertionError("No model/Pi execution allowed")))
        self.directory = compare.prepare("sha256:" + "a" * 64, 2)

    def preflight(self, run):
        run.result.update(runtime={"podman_version": "test"}, seccomp_sha256="a" * 64)

    def test_plan_precedes_runs_pins_inputs_and_balances_order(self):
        plan = compare.load_plan(self.directory)
        self.assertFalse((self.directory / "progress.json").exists())
        self.assertEqual(len(plan["schedule"]), 8)
        self.assertEqual([cell["arm"] for cell in plan["schedule"][:2]], ["stop", "automatic"])
        self.assertEqual([cell["arm"] for cell in plan["schedule"][4:6]], ["automatic", "stop"])
        self.assertIn("recover.py", plan["sources"])
        self.assertIn("go_version", plan)
        self.assertIn("primary_metric", plan)

    def test_changed_source_or_plan_refuses_execution_before_any_attempt(self):
        with patch("compare.sources", return_value={}):
            with self.assertRaisesRegex(ValueError, "inputs changed"):
                compare.run_comparison(self.directory)
        self.assertFalse((self.directory / "progress.json").exists())
        saved = json.loads((self.directory / "plan.json").read_text())
        saved["definition"]["repeats"] = 3
        durable_json(self.directory / "plan.json", saved)
        with self.assertRaisesRegex(ValueError, "inputs changed"):
            compare.run_comparison(self.directory)

    def test_repeated_pairs_keep_control_and_wrong_repairs_without_model_calls(self):
        compare.run_comparison(self.directory)
        result = compare.summarize(self.directory)
        self.loop.assert_not_called()
        self.assertEqual(result["totals"]["stop"]["verified_deliveries"], 0)
        self.assertEqual(result["totals"]["automatic"]["verified_deliveries"], 2)
        self.assertEqual(result["totals"]["stop"]["outcomes"], {"budget_exhausted": 4})
        self.assertEqual(len(result["rows"]), 8)
        self.assertFalse(result["promotion_blocked"])
        wrong = [row for row in result["rows"] if row["case"] == "wrong" and row["arm"] == "automatic"]
        self.assertTrue(all(row["verification_attempts"][-1]["outcome"] == "repair_failed" for row in wrong))
        with self.assertRaises(FileExistsError):
            compare.run_comparison(self.directory)

    def test_stopped_experiment_retains_pending_schedule_and_refuses_blind_restart(self):
        def interrupted(plan, cell, progress, directory):
            cell["status"] = "started"
            durable_json(directory / "progress.json", progress)
            raise Stop("cancelled", "deliberate stop")

        with patch("compare.run_cell", side_effect=interrupted), self.assertRaises(Stop):
            compare.run_comparison(self.directory)
        result = compare.summarize(self.directory)
        self.assertTrue(result["promotion_blocked"])
        self.assertEqual(len(result["rows"]), 8)
        self.assertEqual(result["rows"][0]["status"], "started")
        self.assertEqual(result["rows"][-1]["status"], "pending")
        with self.assertRaises(FileExistsError):
            compare.run_comparison(self.directory)

    def test_altered_schedule_cannot_drop_failed_attempts(self):
        compare.run_comparison(self.directory)
        progress = json.loads((self.directory / "progress.json").read_text())
        progress["cells"].pop()
        durable_json(self.directory / "progress.json", progress)
        with self.assertRaisesRegex(ValueError, "frozen schedule"):
            compare.summarize(self.directory)

    def test_changed_receipt_or_settings_prevent_promotion(self):
        compare.run_comparison(self.directory)
        progress = json.loads((self.directory / "progress.json").read_text())
        cell = next(cell for cell in progress["cells"] if cell["arm"] == "automatic" and cell["case"] == "good")
        path = self.root / cell["receipt"]
        result = json.loads(path.read_text())
        result["budgets"]["model_requests"] += 1
        durable_json(path, result)
        cell["receipt_sha256"] = digest(path.read_bytes())
        durable_json(self.directory / "progress.json", progress)
        report = compare.summarize(self.directory)
        self.assertTrue(report["promotion_blocked"])
        self.assertEqual(report["totals"]["automatic"]["verified_deliveries"], 1)

    def test_changed_check_output_blocks_a_previously_successful_delivery(self):
        compare.run_comparison(self.directory)
        result = compare.summarize(self.directory)
        row = next(row for row in result["rows"] if row["verified_success"])
        child = row["verification_attempts"][0]["verification_run_id"]
        (self.root / "verification/runs" / child / "candidate.stdout").write_text("Deliberately changed check output")
        report = compare.summarize(self.directory)
        self.assertTrue(report["promotion_blocked"])
        self.assertEqual(report["totals"]["automatic"]["verified_deliveries"], 1)

    def test_failed_check_evidence_is_rechecked_before_reporting_a_deliberate_failure(self):
        compare.run_comparison(self.directory)
        result = compare.summarize(self.directory)
        row = next(row for row in result["rows"] if row["case"] == "wrong" and row["arm"] == "automatic")
        child = row["verification_attempts"][0]["verification_run_id"]
        (self.root / "verification/runs" / child / "candidate.stdout").write_text("Deliberately changed failed-check output")
        report = compare.summarize(self.directory)
        self.assertTrue(report["promotion_blocked"])
        changed = next(item for item in report["rows"] if item["receipt"] == row["receipt"])
        self.assertTrue(changed["unconfirmed_completion"])

    def test_a_known_wrong_repair_cannot_be_promoted_even_if_a_checker_claims_success(self):
        compare.run_comparison(self.directory)
        with patch("evaluate.verified", return_value=True):
            report = compare.summarize(self.directory)
        self.assertTrue(report["promotion_blocked"])
        wrong = [row for row in report["rows"] if row["case"] == "wrong"]
        self.assertTrue(all(row["unconfirmed_completion"] and not row["verified_success"] for row in wrong))


if __name__ == "__main__":
    unittest.main()
