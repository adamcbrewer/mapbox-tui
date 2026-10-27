import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import evaluate
import operator_log
from pi_run import scripted_calls
from recovery import Journal
from run import Stop, digest, write_json
from test_repair import RepairFixture


class InventoryTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)

    def receipt(self, identity, result):
        path = self.root / "pi/runs" / identity / "result.json"
        path.parent.mkdir(parents=True)
        write_json(path, result)
        return path

    def test_all_failure_outcomes_and_missing_receipts_remain_visible(self):
        outcomes = ("repair_failed", "blocked", "cancelled", "timeout", "budget_exhausted", "conflict", "invalid_input")
        for number, outcome in enumerate(outcomes):
            self.receipt(str(number), {"outcome": outcome})
        broken = self.receipt("broken", {})
        broken.write_text("not JSON")
        self.receipt("missing", {}).unlink()
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["runs"], 9)
        self.assertEqual(value["summary"]["outcomes"]["evidence_unreadable"], 2)
        self.assertEqual(value["summary"]["eventual_successes"], 0)
        self.assertFalse(value["controlled_comparison"])
        for outcome in outcomes:
            self.assertEqual(value["summary"]["outcomes"][outcome], 1)

    def test_missing_billing_is_unknown_and_receipt_bytes_identified(self):
        path = self.receipt("model", {"outcome": "blocked", "candidate_source": "AI", "model": "example",
                                     "model_cost_usd": None, "duration_seconds": 12})
        row = evaluate.record(path, self.root)
        self.assertEqual(row["receipt_sha256"], digest(path.read_bytes()))
        self.assertEqual(row["receipt_snapshot"], json.loads(path.read_text()))
        self.assertIsNone(row["model_cost_usd"])
        self.assertIsNone(row["execution_cost_usd"])
        self.assertIsNone(row["review_seconds"])
        self.assertEqual(row["mode"], "live")
        self.assertIn("budgets", row["missing_pins"])

    def test_post_run_review_uses_a_bound_supplement_without_rewriting_the_receipt(self):
        path = self.receipt("late-review", {"outcome": "blocked", "operator": {"attention_seconds": None}})
        raw = path.read_bytes()
        operator_log.create(path.parent)
        operator_log.close(path.parent)
        with patch("operator_log.time.monotonic", return_value=10):
            operator_log.append(path.parent, "reviewing")
        with patch("operator_log.time.monotonic", return_value=17):
            operator_log.append(path.parent, "finished")
        row = evaluate.record(path, self.root)
        self.assertEqual(row["review_seconds"], 7)
        self.assertEqual(row["attention_seconds"], 7)
        self.assertEqual(row["operator_evidence"]["sha256"], digest((path.parent / "operator.json").read_bytes()))
        self.assertEqual(row["operator_evidence"]["record"]["events"][-1]["phase"], "review")
        self.assertEqual(path.read_bytes(), raw)

    def test_unreadable_human_supplement_does_not_erase_checked_repair_evidence(self):
        path = self.receipt("bad-review", {"outcome": "native_verification_passed"})
        (path.parent / "operator.json").write_text("not JSON")
        with patch("evaluate.verified", return_value=True):
            row = evaluate.record(path, self.root)
        self.assertTrue(row["verified_success"])
        self.assertIsNone(row["attention_seconds"])
        self.assertIn("problem", row["operator_evidence"])

    def test_focused_pass_and_model_done_are_not_verified_success(self):
        self.receipt("focused", {"outcome": "live_repair_passed", "model_claim": "done"})
        self.receipt("claim", {"outcome": "verified_repair_passed"})
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["eventual_successes"], 0)
        self.assertEqual(value["summary"]["unconfirmed_completions"], 1)
        self.assertIn("I couldn't confirm this saved pass", evaluate.render(value))

    def test_cli_keeps_previous_inventory_and_flags_unconfirmed_completion(self):
        self.receipt("claim", {"outcome": "verified_repair_passed"})
        output = self.root / "output"
        args = ["evaluate.py", "--reports", str(self.root), "--output", str(output)]
        with patch("sys.argv", args):
            self.assertEqual(evaluate.main(), 1)
        original = (output / "inventory.json").read_bytes()
        with patch("sys.argv", args), self.assertRaises(SystemExit) as error:
            evaluate.main()
        self.assertEqual(error.exception.code, 2)
        self.assertEqual((output / "inventory.json").read_bytes(), original)

    def test_empty_inventory_explains_no_experiment_or_model_ran(self):
        value = evaluate.inventory(self.root)
        text = evaluate.render(value)
        self.assertEqual(value["summary"]["runs"], 0)
        self.assertIn("No local runs found", text)
        self.assertIn("makes no model calls", text)
        self.assertIn("not a controlled comparison", text)

    def test_unreadable_receipt_returns_failure_with_evidence_retained(self):
        path = self.receipt("broken", {})
        path.write_text("partial receipt")
        with patch("sys.argv", ["evaluate.py", "--reports", str(self.root)]):
            self.assertEqual(evaluate.main(), 1)
        self.assertEqual(path.read_text(), "partial receipt")

    def test_simulated_checks_are_explicit_in_the_readable_report(self):
        self.receipt("prepared", {"outcome": "blocked", "evidence_origin": "simulated-check-output"})
        self.assertIn("No model or Go checks ran", evaluate.render(evaluate.inventory(self.root)))

    def test_linked_receipt_is_retained_as_unreadable(self):
        path = self.receipt("linked", {"outcome": "blocked"})
        saved = self.root / "saved.json"
        path.rename(saved)
        path.symlink_to(saved)
        self.assertEqual(evaluate.inventory(self.root)["summary"]["outcomes"], {"evidence_unreadable": 1})

    def test_native_workflow_attempts_count_one_task_and_unknown_attention_stays_unknown(self):
        root = self.root / "workflows/runs" / ("a" * 32)
        root.mkdir(parents=True)
        attempts = []
        for index, outcome in enumerate(("repair_failed", "repository_verification_passed"), 1):
            identity = str(index) * 32
            child = self.root / "verification/runs" / identity
            child.mkdir(parents=True)
            write_json(child / "result.json", {"parent_run_id": root.name, "outcome": outcome})
            attempts.append({"run_id": identity, "outcome": outcome})
        write_json(root / "result.json", {"workflow": "native-repair-workflow", "outcome": "native_verification_passed",
                                          "candidate_source": "supplied", "verification_attempts": attempts,
                                          "first_repair_passed": False, "operator": None})
        with patch("evaluate.verified", side_effect=lambda result, *_: result.get("workflow") == "native-repair-workflow"):
            value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["runs"], 1)
        self.assertEqual(value["summary"]["child_receipts"], 2)
        self.assertEqual(value["summary"]["first_attempt_successes"], 0)
        self.assertEqual(value["summary"]["eventual_successes"], 1)
        row = next(row for row in value["records"] if not row["is_child"])
        self.assertEqual(row["mode"], "prepared")
        self.assertEqual(row["verification_attempts"], attempts)
        self.assertIsNone(row["review_seconds"])
        self.assertIsNone(row["attention_seconds"])
        self.assertIsNone(row["user_corrections"])

    def test_native_workflow_claim_without_plan_or_receipts_is_not_a_verified_repair(self):
        root = self.root / "workflows/runs" / ("a" * 32)
        root.mkdir(parents=True)
        write_json(root / "result.json", {"workflow": "native-repair-workflow", "outcome": "native_verification_passed",
                                          "independent_checks_passed": True})
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["eventual_successes"], 0)
        self.assertEqual(value["summary"]["unconfirmed_completions"], 1)


class VerifiedInventoryTests(RepairFixture, unittest.TestCase):
    def finish(self, demo):
        self.dispatch(demo)
        try:
            self.tools.finish()
        except Stop as error:
            self.run.result.update(outcome=error.outcome)
        write_json(self.run.path / "result.json", self.run.result)

    def test_failed_first_repair_and_successful_retry_count_one_task(self):
        self.finish("retry")
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["runs"], 1)
        self.assertEqual(value["summary"]["child_receipts"], 2)
        self.assertEqual(value["summary"]["first_attempt_successes"], 0)
        self.assertEqual(value["summary"]["eventual_successes"], 1)
        root = next(row for row in value["records"] if row["parent_run_id"] is None)
        self.assertEqual([item["outcome"] for item in root["verification_attempts"]],
                         ["repair_failed", "repository_verification_passed"])

    def test_two_failed_repairs_stay_in_denominator(self):
        self.finish("exhausted")
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["runs"], 1)
        self.assertEqual(value["summary"]["outcomes"], {"budget_exhausted": 1})
        self.assertEqual(value["summary"]["eventual_successes"], 0)

    def test_first_repair_success_is_separate_from_retry_success(self):
        for call in scripted_calls(self.tools, "good"):
            self.tools.dispatch(call["name"], call["args"])
        self.tools.finish()
        write_json(self.run.path / "result.json", self.run.result)
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["first_attempt_successes"], 1)
        self.assertEqual(value["summary"]["eventual_successes"], 1)

    def test_unreadable_child_is_not_counted_as_an_extra_task(self):
        self.finish("retry")
        child = self.run.result["attempts"][-1]["verification_run_id"]
        (self.root / "verification/runs" / child / "result.json").write_text("interrupted write")
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["runs"], 1)
        self.assertEqual(value["summary"]["child_receipts"], 2)
        self.assertEqual(value["summary"]["eventual_successes"], 0)

    def test_malformed_attempts_preserve_the_original_completion_claim(self):
        self.finish("retry")
        self.run.result["attempts"] = [None]
        write_json(self.run.path / "result.json", self.run.result)
        row = evaluate.record(self.run.path / "result.json", self.root)
        self.assertTrue(row["unconfirmed_completion"])
        self.assertFalse(row["verified_success"])
        self.assertEqual(row["receipt_snapshot"]["attempts"], [None])

    def test_recovery_success_needs_a_matching_complete_journal(self):
        self.finish("retry")
        self.run.result["recovery"] = True
        write_json(self.run.path / "result.json", self.run.result)
        row = evaluate.record(self.run.path / "result.json", self.root)
        self.assertTrue(row["unconfirmed_completion"])
        self.assertFalse(row["verified_success"])

    def test_matching_recovery_journal_can_confirm_saved_success(self):
        self.finish("retry")
        self.run.result["recovery"] = True
        journal = Journal(self.run, self.checkout)
        journal.state["status"] = "complete"
        journal.save()
        self.assertTrue(evaluate.record(self.run.path / "result.json", self.root)["verified_success"])
        (self.run.path / "recovery-stop.json").write_text('{}')
        self.assertFalse(evaluate.record(self.run.path / "result.json", self.root)["verified_success"])

    def test_invalid_saved_live_settings_block_success_without_aborting_inventory(self):
        self.finish("retry")
        self.run.result.update(recovery=True, live_config={"invalid": True})
        journal = Journal(self.run, self.checkout)
        journal.state.update(status="complete", live_config={"invalid": True})
        journal.save()
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["eventual_successes"], 0)
        self.assertEqual(value["summary"]["unconfirmed_completions"], 1)

    def test_changed_check_log_prevents_success_without_rewriting_receipt(self):
        self.finish("retry")
        receipt = (self.run.path / "result.json").read_bytes()
        child = self.run.result["attempts"][-1]["verification_run_id"]
        (self.root / "verification/runs" / child / "candidate.stdout").write_text("fabricated pass")
        value = evaluate.inventory(self.root)
        self.assertEqual(value["summary"]["eventual_successes"], 0)
        self.assertEqual(value["summary"]["unconfirmed_completions"], 2)
        self.assertEqual((self.run.path / "result.json").read_bytes(), receipt)

    def test_changed_settings_form_a_different_fingerprint(self):
        self.finish("retry")
        path = self.run.path / "result.json"
        before = evaluate.record(path, self.root)
        self.run.result["model"] = "different-model"
        write_json(path, self.run.result)
        after = evaluate.record(path, self.root)
        self.assertNotEqual(before["settings_sha256"], after["settings_sha256"])


if __name__ == "__main__":
    unittest.main()
