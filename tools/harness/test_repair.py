import copy
import json
from pathlib import Path
import signal
import tempfile
import unittest
from unittest.mock import patch

from container import ContainerRun
from pi_run import MAX_CALLS, prepare_checkout
from repair import RepairRun, RepairTools, repair_receipt_matches
from run import HERE, Stop, digest, now, write_json
from verify import REGRESSION, REGRESSION_CHECKS
import report


class RepairFixture:
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.checkout = self.root / "mapbox-calibration-repair-test"
        self.checkout.mkdir()
        self.run = RepairRun(self.root / "repair", "sha256:" + "a" * 64)
        self.run.result.update(workflow="pi-verified-repair", budgets={"repair_attempts": 2})
        self.enterContext(patch("repair.REPORTS", self.root))
        self.enterContext(patch("verify.preflight"))
        self.enterContext(patch.object(ContainerRun, "prepare", lambda run: setattr(run, "prepared", True)))
        self.enterContext(patch.object(ContainerRun, "execute", autospec=True, side_effect=self.execute))
        self.run.prepare()
        prepare_checkout(self.run, self.checkout)
        self.tools = RepairTools(self.run, self.checkout)

    def execute(self, run, name, args, cwd, build_output=False):
        if name == "go-version":
            return {"exit_code": 0}, b"go version go1.27.1 linux/amd64\n"
        task = REGRESSION if name in REGRESSION_CHECKS else run.task
        broken = 'if n >= 3 && string(header[:3])' not in (Path(cwd) / "go/import.go").read_text()
        action = "fail" if broken else "pass"
        output = b"\n".join(json.dumps({"Action": action, "Test": test}).encode() for test in
                            [task["test"], *(task["test"] + "/" + case for case in task["cases"])])
        if broken:
            output += b"\n" + json.dumps({"Action": "output", "Output": task["failure_message"]}).encode()
        if name in ("candidate-format", "candidate-suite"):
            output, action = b"Required check passed", "pass"
        check = {"id": name, "command": args, "started_at": now(), "ended_at": now(), "duration_seconds": 0.01,
                 "exit_code": 1 if action == "fail" else 0, "outcome": "failed" if action == "fail" else "passed",
                 "stdout": name + ".stdout", "stderr": name + ".stderr"}
        (run.path / check["stdout"]).write_bytes(output)
        (run.path / check["stderr"]).write_bytes(b"")
        run.result["checks"].append(check)
        run.event("command_finished", check=check)
        return check, output

    def dispatch(self, demo, count=None):
        calls = self.tools.prepared_calls(demo)
        return [self.tools.dispatch(call["name"], call["args"]) for call in calls[:count]]


class RepairTests(RepairFixture, unittest.TestCase):
    def changed_rules(self):
        read_bytes = Path.read_bytes

        def changed(path):
            content = read_bytes(path)
            if path == HERE / "fixtures/import_verifier_test.go.txt":
                return content + b"\n// Changed verification rules\n"
            return content

        return patch.object(Path, "read_bytes", changed)

    def assert_changed_rules_stop(self, completed_calls):
        self.dispatch("retry", completed_calls)
        with self.changed_rules(), patch.object(ContainerRun, "execute") as execute, self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        self.assertEqual(error.exception.outcome, "invalid_input")
        self.assertIn("Verification rules changed", error.exception.reason)
        execute.assert_not_called()
        self.assertFalse(self.tools.edit_available())
        self.assertEqual(self.tools.attempts[-1]["outcome"], "invalid_input")

    def test_rule_change_before_first_check_stops_without_retry(self):
        self.assert_changed_rules_stop(2)

    def test_rule_change_between_attempts_stops_without_retry(self):
        self.assert_changed_rules_stop(5)

    def test_rule_change_after_checks_prevents_final_promotion(self):
        self.dispatch("retry")
        with self.changed_rules(), self.assertRaises(Stop) as error:
            self.tools.finish()
        self.assertEqual(error.exception.outcome, "invalid_input")
        self.assertNotEqual(self.run.result["outcome"], "verified_repair_passed")

    def test_parent_gate_requires_the_frozen_evaluator_on_every_child(self):
        self.dispatch("retry")
        self.tools.finish()
        item = self.tools.attempts[-1]
        path = self.root / "verification/runs" / item["verification_run_id"] / "result.json"
        child = json.loads(path.read_text())
        child["evaluator_sha256"] = "different-rules"
        write_json(path, child)
        item["receipt_sha256"] = digest(path.read_bytes())
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_failed_check_returns_real_evidence_and_retry_passes_only_latest_patch(self):
        results = self.dispatch("retry")
        self.assertEqual(results[2]["outcome"], "repair_failed")
        self.assertEqual(results[2]["remaining_attempts"], 1)
        self.assertEqual(results[2]["checks"][-1]["id"], "candidate")
        self.assertIn("UTF-8 BOM", results[2]["diagnostics"]["stdout"])
        self.assertEqual(results[5]["outcome"], "passed")
        self.assertEqual(results[5]["remaining_attempts"], 0)
        self.assertNotEqual(results[2]["patch_sha256"], results[5]["patch_sha256"])
        self.tools.finish()
        self.assertTrue(repair_receipt_matches(self.run.result, self.run.path, self.root))
        self.assertEqual(self.run.result["outcome"], "verified_repair_passed")
        self.assertEqual([a["attempt"] for a in self.tools.attempts], [1, 2])
        self.assertFalse(self.tools.edit_available())
        self.assertFalse(self.tools.check_available())
        for item in self.tools.attempts:
            child = json.loads((self.root / "verification/runs" / item["verification_run_id"] / "result.json").read_text())
            self.assertEqual(child["parent_run_id"], self.run.path.name)
            self.assertTrue(all(c["attempt"] == item["attempt"] for c in child["checks"]))

    def test_two_failures_exhaust_budget_and_third_attempt_is_refused(self):
        results = self.dispatch("exhausted")
        self.assertEqual(results[-1]["outcome"], "repair_failed")
        self.assertEqual(results[-1]["remaining_attempts"], 0)
        for call in self.tools.prepared_calls("exhausted")[3:]:
            if call["name"] == "harness_edit":
                self.assertTrue(self.tools.dispatch(call["name"], call["args"])["replayed"])
                self.assertEqual(self.tools.dispatch(call["name"], {**call["args"], "operation_id": "repair-3"})["outcome"], "rejected")
            elif call["name"] == "harness_check":
                self.assertEqual(self.tools.dispatch(call["name"], call["args"])["outcome"], "rejected")
        with self.assertRaises(Stop) as error:
            self.tools.finish()
        self.assertEqual(error.exception.outcome, "budget_exhausted")
        self.run.result.update(outcome=error.exception.outcome)
        self.assertEqual(len(self.tools.attempts), 2)
        self.assertTrue(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_environment_failures_do_not_unlock_a_retry_and_keep_receipts(self):
        for outcome in ("blocked", "timeout", "invalid_input"):
            with self.subTest(outcome=outcome):
                self.tools = RepairTools(self.run, self.checkout)
                self.run.result["attempts"].clear()
                self.dispatch("retry", 2)
                with patch("verify.preflight", side_effect=Stop(outcome, "Environment cannot complete checks")), self.assertRaises(Stop) as error:
                    self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
                self.assertEqual(error.exception.outcome, outcome)
                self.assertFalse(self.tools.edit_available())
                self.assertEqual(len(self.tools.attempts), 1)
                self.assertEqual(self.tools.attempts[0]["outcome"], outcome)
                (self.checkout / "go/import.go").write_text(self.tools.seeded)

    def test_no_check_without_edit_or_two_edits_without_check(self):
        self.assertEqual(self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})["outcome"], "rejected")
        calls = self.tools.prepared_calls("retry")
        self.assertEqual(self.tools.dispatch(calls[1]["name"], calls[1]["args"])["outcome"], "ok")
        edit = {**calls[1]["args"], "operation_id": "other"}
        self.assertEqual(self.tools.dispatch("harness_edit", edit)["outcome"], "rejected")

    def test_verification_for_a_different_task_does_not_unlock_retry(self):
        self.dispatch("retry", 2)
        self.run.result["task_sha256"] = "different-task"
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        self.assertEqual(error.exception.outcome, "invalid_input")
        self.assertFalse(self.tools.edit_available())

    def test_saying_done_or_stopping_before_retry_cannot_pass(self):
        self.run.result["model_claim"] = "Done. All tests passed."
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))
        self.dispatch("retry", 3)
        with self.assertRaises(Stop) as error:
            self.tools.finish()
        self.assertEqual(error.exception.outcome, "incomplete")

    def test_stale_patch_and_child_receipts_cannot_satisfy_parent_gate(self):
        self.dispatch("retry")
        self.tools.finish()
        for mutate in (
            lambda r: r["attempts"][1].update(verification_run_id=r["attempts"][0]["verification_run_id"]),
            lambda r: r["attempts"][1].update(receipt_sha256="stale"),
            lambda r: r["attempts"][1].update(patch_sha256=r["attempts"][0]["patch_sha256"]),
        ):
            result = copy.deepcopy(self.run.result)
            mutate(result)
            self.assertFalse(repair_receipt_matches(result, self.run.path, self.root))
        (self.run.path / "candidate.patch").write_text("different patch")
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_report_preserves_both_attempts_and_refuses_changed_final_evidence(self):
        self.run.result["candidate"] = "retry"
        self.dispatch("retry")
        self.tools.finish()
        write_json(self.run.path / "result.json", self.run.result)
        page = report.render_repair(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("Attempt 1: repair failed the original import test", page)
        self.assertIn("Attempt 2: passed all eight independent checks", page)
        self.assertIn("No AI model ran", page)
        progress = {"stage": "Verify", "status": "awaiting-review", "schema_version": 1,
                    "source": "Synthetic test",
                    "updated_at": "2026-10-23", "next": "Review", "decisions": [], "blockers": [], "questions": []}
        report.save_stage(self.root / "saved", progress, self.root, source_ref="a" * 40)
        self.assertIn("working retry", report.render_saved(self.root / "saved", "verify").read_text())
        (self.run.path / "candidate.patch").write_text("changed")
        page = report.render_repair(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("Evidence needs inspection", page)
        self.assertNotIn('badge pass', page)

    def test_retry_uses_the_shared_deadline_and_tool_budget(self):
        self.dispatch("retry", 2)

        def expired(child):
            self.assertEqual(child.deadline, self.run.deadline)
            raise Stop("timeout", "Shared deadline reached")

        with patch("verify.preflight", side_effect=expired), self.assertRaises(Stop):
            self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        self.tools.calls = MAX_CALLS
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_read", {"file": "go/import.go"})
        self.assertEqual(error.exception.outcome, "budget_exhausted")

    def test_cancellation_records_child_and_restores_signal_ownership(self):
        self.dispatch("retry", 2)
        before = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}

        def cancel(_child):
            signal.getsignal(signal.SIGTERM)(signal.SIGTERM, None)

        with patch("verify.preflight", side_effect=cancel), self.assertRaises(KeyboardInterrupt):
            self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        self.assertEqual(self.tools.attempts[0]["outcome"], "cancelled")
        for signum, handler in before.items():
            self.assertEqual(signal.getsignal(signum), handler)

if __name__ == "__main__":
    unittest.main()
