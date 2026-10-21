import json
from pathlib import Path
import sys
import tempfile
import time
import unittest

from report import render, render_run
from run import HERE, Run, Stop, apply_patch, classify_test


class CalibrationTests(unittest.TestCase):
    def setUp(self):
        self.task = json.loads((HERE / "task.json").read_text())
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_false_completion_and_empty_selection_are_not_passes(self):
        for output in (b"done", b"PASS", b"", b'{"Action":"pass","Package":"mapbox-tui/go"}'):
            with self.subTest(output=output):
                self.assertEqual(classify_test(output, 0, self.task), "blocked")
        test = self.task["test"]
        events = [{"Action": "pass", "Test": name} for name in
                  [test, *(test + "/" + case for case in self.task["cases"])]]
        output = b"\n".join(json.dumps(event).encode() for event in events)
        self.assertEqual(classify_test(output, 0, self.task), "passed")
        self.assertEqual(classify_test(output, 1, self.task), "blocked")

    def test_assertion_failure_differs_from_build_failure(self):
        failure = json.dumps({"Action": "fail", "Test": self.task["test"]}).encode()
        self.assertEqual(classify_test(failure, 1, self.task), "assertion_failed")
        self.assertEqual(classify_test(b'{"Action":"fail"}', 1, self.task), "blocked")

    def test_rejects_patch_to_verifier(self):
        run = Run(self.root)
        patch = (HERE / "fixtures/good.patch").read_bytes().replace(b"go/import.go", b"go/import_edge_test.go")
        with self.assertRaises(Stop) as caught:
            apply_patch(run, self.root, patch, "bad", self.task["allowed_files"])
        self.assertEqual(caught.exception.outcome, "invalid_input")

    def test_missing_executable_is_blocked(self):
        run = Run(self.root)
        with self.assertRaises(Stop) as caught:
            run.command("missing", [str(self.root / "absent")], self.root)
        self.assertEqual(caught.exception.outcome, "blocked")
        self.assertEqual(run.result["checks"][0]["outcome"], "blocked")
        self.assertIn("command_finished", (run.path / "events.jsonl").read_text())

    def test_deadline_keeps_partial_evidence(self):
        run = Run(self.root)
        run.command("completed", [sys.executable, "-c", "print('completed')"], self.root)
        run.deadline = time.monotonic() + 0.1
        with self.assertRaises(Stop) as caught:
            run.command("slow", [sys.executable, "-c", "import time; time.sleep(10); print('too late')"], self.root)
        self.assertEqual(caught.exception.outcome, "timeout")
        self.assertIn("completed", (run.path / "completed.stdout").read_text())
        self.assertEqual((run.path / "slow.stdout").read_text(), "")
        self.assertEqual(json.loads((run.path / "result.json").read_text())["outcome"], "incomplete")
        self.assertEqual(run.result["checks"][-1]["outcome"], "timeout")

    def test_empty_report_and_unreadable_record_are_honest(self):
        progress = json.loads((HERE / "progress.json").read_text())
        page = render(self.root, progress)
        self.assertIn("No runs recorded", page)
        self.assertIn("No comparisons yet", page)
        run = Run(self.root)
        (run.path / "result.json").write_text("broken")
        self.assertIn("Unreadable record", render(self.root, progress))

    def test_report_escapes_records_and_does_not_promote_review(self):
        progress = json.loads((HERE / "progress.json").read_text())
        progress["status"] = "awaiting-review"
        run = Run(self.root)
        run.result.update(candidate="<script>alert(1)</script>", outcome="calibration_passed")
        (run.path / "result.json").write_text(json.dumps(run.result))
        page = render(self.root, progress)
        self.assertNotIn("<script>", page)
        self.assertIn("Waiting for our review", page)
        self.assertIn("Time not recorded", page)

    def test_report_orders_by_run_start_and_keeps_failed_runs(self):
        progress = json.loads((HERE / "progress.json").read_text())
        for identifier, date, outcome in [("a", "2026-10-22", "calibration_passed"),
                                          ("z", "2026-10-21", "repair_failed")]:
            path = self.root / "runs" / identifier
            path.mkdir(parents=True)
            (path / "result.json").write_text(json.dumps({
                "schema_version": 1, "started_at": date, "checks": [], "outcome": outcome,
            }))
        page = render(self.root, progress)
        self.assertLess(page.index("repair_failed"), page.index("calibration_passed"))

    def test_report_explains_expected_failure_without_disguising_a_blocker(self):
        path = self.root / "runs/1234567890/result.json"
        run = {"schema_version": 1, "candidate": "wrong", "outcome": "repair_failed", "checks": []}
        page = render_run(path, self.root, run)
        self.assertIn("This failure is the expected answer", page)
        self.assertIn("did not pass this check", page)
        self.assertIn('href="runs/1234567890/result.json"', page)
        self.assertIn('>12345678</code>', page)
        run.update(outcome="blocked")
        page = render_run(path, self.root, run)
        self.assertNotIn("This failure is the expected answer", page)
        self.assertIn("does not tell us whether the repair works", page)

    def test_report_does_not_present_unknown_record_as_success(self):
        path = self.root / "runs/example/result.json"
        for checks in (None, ["not a check"]):
            page = render_run(path, self.root, {
                "schema_version": 1, "candidate": "good", "outcome": "calibration_passed", "checks": checks,
            })
            self.assertIn("Unreadable record", page)
            self.assertNotIn("The proposed repair passed this check.", page)

    def test_report_does_not_judge_repairs_when_setup_fails(self):
        path = self.root / "runs/example/result.json"
        for fault, outcome, blocker, explanation in (
            ("missing-source", "blocked", "missing_source", "source file was missing"),
            ("missing-dependency", "blocked", "missing_dependency", "dependencies were unavailable"),
            ("timeout", "timeout", None, "stopped at its time limit"),
        ):
            with self.subTest(fault=fault):
                run = {"schema_version": 1, "candidate": "good", "fault": fault,
                       "outcome": outcome, "blocker": blocker, "checks": []}
                page = render_run(path, self.root, run)
                self.assertIn(explanation, page)
                self.assertIn("repair has not been judged", page)
                self.assertNotIn("The proposed repair passed this check.", page)
                run.update(outcome="blocked", blocker=None, reason="Missing executable: go")
                page = render_run(path, self.root, run)
                self.assertIn("Go was unavailable", page)
                self.assertNotIn(explanation, page)


if __name__ == "__main__":
    unittest.main()
