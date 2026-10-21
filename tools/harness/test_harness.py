import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

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

    def test_unlaunchable_tools_are_blocked(self):
        tool = self.root / "tool"
        tool.write_text("not an executable format\n")
        for mode in (0o644, 0o755):
            with self.subTest(mode=mode):
                tool.chmod(mode)
                run = Run(self.root)
                with self.assertRaises(Stop) as caught:
                    run.command("tool", [str(tool)], self.root)
                self.assertEqual(caught.exception.outcome, "blocked")
                self.assertEqual(run.result["checks"][0]["outcome"], "blocked")
                self.assertIn("command_finished", (run.path / "events.jsonl").read_text())

    def test_termination_stops_command_group_and_finishes_receipt(self):
        tool = self.root / "go"
        tool.write_text(f"#!{sys.executable}\n" +
                        "import json, os, subprocess, sys, time\n"
                        "child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])\n"
                        "print(json.dumps([os.getpid(), child.pid]), flush=True)\n"
                        "time.sleep(60)\n")
        tool.chmod(0o755)
        for termination in (signal.SIGTERM, signal.SIGINT):
            with self.subTest(signal=termination):
                reports = self.root / str(termination)
                command = [sys.executable, "-c",
                           "import sys; from pathlib import Path; import run; "
                           "run.REPORTS = Path(sys.argv[1]); sys.argv = ['run.py', '--demo', 'empty']; "
                           "raise SystemExit(run.main())", str(reports)]
                process = subprocess.Popen(command, cwd=HERE, env={**os.environ, "PATH": str(self.root)},
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                pids = []
                try:
                    deadline = time.monotonic() + 10
                    while time.monotonic() < deadline:
                        paths = list(reports.glob("runs/*/go-version.stdout"))
                        if paths and paths[0].read_text().strip():
                            pids = json.loads(paths[0].read_text())
                            break
                        if process.poll() is not None:
                            self.fail(f"Runner exited before starting its tool: {process.communicate()}")
                        time.sleep(0.01)
                    self.assertTrue(pids, "Tool did not announce readiness")
                    process.send_signal(termination)
                    output, errors = process.communicate(timeout=10)
                    self.assertEqual(process.returncode, 1, (output, errors))
                    result = json.loads(paths[0].with_name("result.json").read_text())
                    self.assertEqual(result["outcome"], "cancelled")
                    self.assertEqual(result["checks"][-1]["outcome"], "cancelled")
                    events = [json.loads(line) for line in paths[0].with_name("events.jsonl").read_text().splitlines()]
                    self.assertEqual(events[-1]["kind"], "run_finished")
                    self.assertEqual(events[-1]["outcome"], "cancelled")
                    deadline = time.monotonic() + 5
                    for pid in pids:
                        status = Path(f"/proc/{pid}/stat")
                        while True:
                            try:
                                if status.read_text().split()[2] == "Z":
                                    break
                            except FileNotFoundError:
                                break
                            self.assertLess(time.monotonic(), deadline, f"Process {pid} is still running")
                            time.sleep(0.01)
                finally:
                    if pids:
                        try:
                            os.killpg(pids[0], signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                    if process.poll() is None:
                        process.kill()
                    process.communicate()

    def test_cancellation_during_launch_waits_for_process_handle(self):
        run = Run(self.root)
        launch = subprocess.Popen
        children = []

        def interrupted_launch(*args, **kwargs):
            process = launch(*args, **kwargs)
            children.append(process)
            os.kill(os.getpid(), signal.SIGTERM)
            return process

        previous = signal.signal(signal.SIGTERM, run.cancel)
        try:
            with patch("run.subprocess.Popen", side_effect=interrupted_launch):
                with self.assertRaises(Stop) as caught:
                    run.command("slow", [sys.executable, "-c", "import time; time.sleep(60)"], self.root)
            self.assertEqual(caught.exception.outcome, "cancelled")
            self.assertEqual(run.result["checks"][-1]["outcome"], "cancelled")
            self.assertIsNotNone(children[0].poll())
        finally:
            signal.signal(signal.SIGTERM, previous)
            for child in children:
                if child.poll() is None:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()

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
