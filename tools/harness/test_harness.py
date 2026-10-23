import json
import io
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import run as runner
from report import render, render_run
from run import HERE, Run, Stop, apply_patch, check_test, classify_test


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

    def test_build_failures_depend_on_stage_but_missing_dependencies_stay_blocked(self):
        outputs = (b'{"Action":"build-fail","ImportPath":"mapbox-tui/go"}',
                   b'{"Action":"fail","FailedBuild":"mapbox-tui/go"}',
                   b'{"Action":"output","Output":"FAIL mapbox-tui/go [build failed]"}')
        run = Run(self.root)
        (run.path / "test.stderr").write_bytes(b"")
        for output in outputs:
            for stage in ("baseline", "seed", "candidate"):
                with self.subTest(stage=stage, output=output):
                    check = {"id": stage, "exit_code": 1, "stderr": "test.stderr"}
                    with self.assertRaises(Stop) as caught:
                        check_test(run, stage, check, output, self.task)
                    self.assertEqual(caught.exception.outcome, "repair_failed" if stage == "candidate" else "blocked")
                    self.assertEqual(check["outcome"], "build_failed")
        (run.path / "test.stderr").write_text("module lookup disabled by GOPROXY=off")
        with self.assertRaises(Stop) as caught:
            check_test(run, "candidate", check, outputs[0], self.task)
        self.assertEqual(caught.exception.outcome, "blocked")
        self.assertEqual(check["outcome"], "blocked")
        self.assertEqual(run.result["blocker"], "missing_dependency")

    def test_setup_io_failure_is_blocked_but_unreadable_patch_is_invalid(self):
        with patch("run.REPORTS", self.root), patch("sys.argv", ["run.py", "--demo", "empty"]), \
             patch.object(Run, "command", return_value=({"exit_code": 0}, b"go version")), \
             patch("run.tempfile.TemporaryDirectory", side_effect=OSError("No space left on device")):
            self.assertEqual(runner.main(), 1)
        result = json.loads(next(self.root.glob("runs/*/result.json")).read_text())
        self.assertEqual(result["outcome"], "blocked")
        self.assertIn("No space left", result["reason"])
        reports = self.root / "input"
        with patch("run.REPORTS", reports), \
             patch("sys.argv", ["run.py", "--patch", str(self.root / "missing.patch")]):
            self.assertEqual(runner.main(), 1)
        result = json.loads(next(reports.glob("runs/*/result.json")).read_text())
        self.assertEqual(result["outcome"], "invalid_input")

    def test_signals_during_finalization_do_not_interrupt_receipt_or_event(self):
        write_json = runner.write_json
        event = Run.event
        handlers = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}

        def interrupted_write(path, value):
            if "ended_at" in value:
                os.kill(os.getpid(), signal.SIGTERM)
            write_json(path, value)

        def interrupted_event(run, kind, **fields):
            if kind == "run_finished":
                os.kill(os.getpid(), signal.SIGINT)
            event(run, kind, **fields)

        for outcome in ("calibration_passed", "cancelled", "blocked", "timeout"):
            with self.subTest(outcome=outcome):
                reports = self.root / outcome
                with patch("run.REPORTS", reports), patch("sys.argv", ["run.py", "--demo", "empty"]), \
                     patch("run.calibrate", side_effect=Stop(outcome, "decided result")), \
                     patch("run.write_json", side_effect=interrupted_write), \
                     patch.object(Run, "event", interrupted_event):
                    self.assertEqual(runner.main(), 0 if outcome == "calibration_passed" else 1)
                path = next(reports.glob("runs/*/result.json"))
                self.assertEqual(json.loads(path.read_text())["outcome"], outcome)
                events = [json.loads(line) for line in path.with_name("events.jsonl").read_text().splitlines()]
                self.assertEqual(events[-1]["kind"], "run_finished")
                self.assertEqual(events[-1]["outcome"], outcome)
                for sig, handler in handlers.items():
                    self.assertEqual(signal.getsignal(sig), handler)

    def test_rejects_patch_to_verifier(self):
        run = Run(self.root)
        patch = (HERE / "fixtures/good.patch").read_bytes().replace(b"go/import.go", b"go/import_edge_test.go")
        with self.assertRaises(Stop) as caught:
            apply_patch(run, self.root, patch, "bad", self.task["allowed_files"])
        self.assertEqual(caught.exception.outcome, "invalid_input")

    def test_patch_source_text_is_not_treated_as_file_metadata(self):
        source = self.root / "go/import.go"
        source.parent.mkdir()
        before = 'var label = "old mode; new file mode; deleted file mode"\n'
        after = 'var label = "new mode; rename from; copy from"\n'
        source.write_text(before)
        change = ('--- a/go/import.go\n+++ b/go/import.go\n@@ -1 +1 @@\n-' + before + '+' + after).encode()
        run = Run(self.root)
        apply_patch(run, self.root, change, "candidate", self.task["allowed_files"])
        self.assertEqual(source.read_text(), after)

    def test_patch_metadata_still_rejects_file_operations(self):
        source = self.root / "go/import.go"
        source.parent.mkdir()
        source.write_text("package main\n")
        for metadata in (
            "diff --git a/go/import.go b/go/import.go\nold mode 100644\nnew mode 100755\n",
            "--- a/go/import.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package main\n",
            "--- /dev/null\n+++ b/go/import.go\n@@ -0,0 +1 @@\n+package main\n",
            "diff --git a/go/import.go b/go/other.go\nsimilarity index 100%\nrename from go/import.go\nrename to go/other.go\n",
            "diff --git a/go/import.go b/go/other.go\nsimilarity index 100%\ncopy from go/import.go\ncopy to go/other.go\n",
        ):
            with self.subTest(metadata=metadata):
                run = Run(self.root)
                with self.assertRaises(Stop) as caught:
                    apply_patch(run, self.root, metadata.encode(), "candidate", ["go/import.go", "go/other.go"])
                self.assertEqual(caught.exception.outcome, "invalid_input")
                self.assertNotIn("candidate-apply", [check["id"] for check in run.result["checks"]])
                self.assertEqual(source.read_text(), "package main\n")

    def test_unsupported_platforms_save_blocked_without_launching_commands(self):
        for platform in ("win32", "darwin"):
            with self.subTest(platform=platform):
                reports = self.root / platform
                with patch("run.REPORTS", reports), patch("sys.argv", ["run.py", "--demo", "empty"]), \
                     patch("run.sys.platform", platform), patch("run.subprocess.Popen") as launch:
                    self.assertEqual(runner.main(), 1)
                    launch.assert_not_called()
                path = next(reports.glob("runs/*/result.json"))
                result = json.loads(path.read_text())
                self.assertEqual(result["outcome"], "blocked")
                self.assertEqual(result["checks"], [])
                self.assertIn("Linux only", result["reason"])
                events = [json.loads(line) for line in path.with_name("events.jsonl").read_text().splitlines()]
                self.assertEqual(events[-1]["kind"], "run_finished")

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

    def test_output_limit_bounds_stdout_and_stderr_and_stops_noisy_process(self):
        launch = subprocess.Popen
        for streams in ((1,), (2,), (1, 2)):
            with self.subTest(streams=streams):
                run = Run(self.root)
                children = []

                def tracked_launch(*args, **kwargs):
                    process = launch(*args, **kwargs)
                    children.append(process)
                    return process

                script = f"import os\nwhile True:\n for fd in {streams!r}: os.write(fd, b'x' * 4096)"
                with patch("run.subprocess.Popen", side_effect=tracked_launch), self.assertRaises(Stop) as caught:
                    run.command("noisy", [sys.executable, "-c", script], self.root, output_limit=65536)
                self.assertEqual(caught.exception.outcome, "budget_exhausted")
                self.assertEqual(run.result["checks"][-1]["outcome"], "budget_exhausted")
                self.assertEqual(sum((run.path / f"noisy.{stream}").stat().st_size
                                     for stream in ("stdout", "stderr")), 65536)
                for fd in streams:
                    self.assertGreater((run.path / ("noisy.stdout" if fd == 1 else "noisy.stderr")).stat().st_size, 0)
                self.assertIsNotNone(children[0].poll())
                self.assertTrue(children[0].stdout.closed)
                self.assertTrue(children[0].stderr.closed)
                event = json.loads((run.path / "events.jsonl").read_text().splitlines()[-1])
                self.assertEqual(event["kind"], "command_finished")
                self.assertEqual(event["check"]["outcome"], "budget_exhausted")

    def test_bounded_output_preserves_both_streams_up_to_the_exact_limit(self):
        for limit in (12, 13):
            with self.subTest(limit=limit):
                run = Run(self.root)
                check, output = run.command("small", [sys.executable, "-c",
                                                      "import os; os.write(1, b'output'); os.write(2, b'errors')"],
                                            self.root, output_limit=limit)
                self.assertEqual(check["outcome"], "passed")
                self.assertEqual(output, b"output")
                self.assertEqual((run.path / "small.stderr").read_bytes(), b"errors")
                self.assertEqual(check["output_limit_bytes"], limit)

    def test_ready_streams_both_retain_diagnostics_before_budget_exhaustion(self):
        run = Run(self.root)
        stdout_read, stdout_write = os.pipe()
        stderr_read, stderr_write = os.pipe()
        for descriptor, content in ((stdout_write, b"o" * 1024), (stderr_write, b"e" * 1024)):
            os.write(descriptor, content)
            os.close(descriptor)
        with os.fdopen(stdout_read, "rb") as stdout, os.fdopen(stderr_read, "rb") as stderr:
            process = SimpleNamespace(stdout=stdout, stderr=stderr, args=["prepared-output"])
            out, err = io.BytesIO(), io.BytesIO()
            with self.assertRaises(Stop) as caught:
                run.capture_output(process, out, err, output_limit=64)
        self.assertEqual(caught.exception.outcome, "budget_exhausted")
        self.assertEqual(out.getvalue(), b"o" * 32)
        self.assertEqual(err.getvalue(), b"e" * 32)

    def test_bounded_output_keeps_partial_logs_when_the_deadline_expires(self):
        run = Run(self.root)
        run.deadline = time.monotonic() + 0.2
        with self.assertRaises(Stop) as caught:
            run.command("slow", [sys.executable, "-c",
                                 "import os, time; os.write(1, b'output'); os.write(2, b'errors'); time.sleep(60)"],
                        self.root, output_limit=1024)
        self.assertEqual(caught.exception.outcome, "timeout")
        self.assertEqual((run.path / "slow.stdout").read_bytes(), b"output")
        self.assertEqual((run.path / "slow.stderr").read_bytes(), b"errors")

    def test_output_budget_kills_descendants_even_after_the_client_exits(self):
        run = Run(self.root)
        pid_file = self.root / "child.pid"
        child = ("import os, sys, time\n"
                 "while os.getppid() == int(sys.argv[1]): time.sleep(0.001)\n"
                 "while True: os.write(1, b'x' * 4096)")
        script = ("import os, subprocess, sys\n"
                  f"process = subprocess.Popen([sys.executable, '-c', {child!r}, str(os.getpid())])\n"
                  f"open({str(pid_file)!r}, 'w').write(str(process.pid))\n")
        try:
            with self.assertRaises(Stop) as caught:
                run.command("descendant", [sys.executable, "-c", script], self.root, output_limit=65536)
            self.assertEqual(caught.exception.outcome, "budget_exhausted")
            child_pid = int(pid_file.read_text())
            deadline = time.monotonic() + 5
            while True:
                try:
                    if Path(f"/proc/{child_pid}/stat").read_text().split()[2] == "Z":
                        break
                except FileNotFoundError:
                    break
                self.assertLess(time.monotonic(), deadline, "Noisy descendant is still running")
                time.sleep(0.01)
        finally:
            if pid_file.exists():
                try:
                    os.kill(int(pid_file.read_text()), signal.SIGKILL)
                except ProcessLookupError:
                    pass

    def test_cancellation_interrupts_bounded_output_capture(self):
        run = Run(self.root)
        previous = signal.signal(signal.SIGTERM, run.cancel)
        try:
            with self.assertRaises(Stop) as caught:
                run.command("cancel", [sys.executable, "-c",
                                       "import os, signal, time; os.write(1, b'output'); os.kill(os.getppid(), signal.SIGTERM); time.sleep(60)"],
                            self.root, output_limit=1024)
            self.assertEqual(caught.exception.outcome, "cancelled")
            self.assertEqual(run.result["checks"][-1]["outcome"], "cancelled")
        finally:
            signal.signal(signal.SIGTERM, previous)

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

    def test_launch_time_counts_towards_deadline(self):
        run = Run(self.root)
        launch = subprocess.Popen
        children = []

        def slow_launch(*args, **kwargs):
            process = launch(*args, **kwargs)
            children.append(process)
            run.deadline = time.monotonic() - 1
            return process

        try:
            with patch("run.subprocess.Popen", side_effect=slow_launch):
                with self.assertRaises(Stop) as caught:
                    run.command("slow-launch", [sys.executable, "-c", "pass"], self.root)
            self.assertEqual(caught.exception.outcome, "timeout")
            self.assertEqual(run.result["checks"][-1]["outcome"], "timeout")
            self.assertIsNotNone(children[0].poll())
        finally:
            for child in children:
                if child.poll() is None:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()

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
        run = {"schema_version": 1, "candidate": "wrong", "outcome": "repair_failed", "checks": [
            {"id": "baseline", "outcome": "passed", "exit_code": 0},
            {"id": "seed", "outcome": "assertion_failed", "exit_code": 1},
            {"id": "candidate", "outcome": "assertion_failed", "exit_code": 1},
        ]}
        page = render_run(path, self.root, run)
        self.assertIn("This failure is the expected answer", page)
        self.assertIn("did not pass this check", page)
        self.assertIn('href="runs/1234567890/result.json"', page)
        self.assertIn('>12345678</code>', page)
        run.update(outcome="blocked")
        page = render_run(path, self.root, run)
        self.assertNotIn("This failure is the expected answer", page)
        self.assertIn("does not tell us whether the repair works", page)

    def test_report_keeps_latest_blocked_example_in_front_of_earlier_success(self):
        progress = json.loads((HERE / "progress.json").read_text())
        for identifier, date, outcome in [("older-success", "2026-10-21", "calibration_passed"),
                                          ("newer-blocked", "2026-10-22", "blocked")]:
            directory = self.root / "runs" / identifier
            directory.mkdir(parents=True)
            (directory / "result.json").write_text(json.dumps({
                "schema_version": 1, "candidate": "good", "started_at": date,
                "checks": [], "outcome": outcome, "execution_mode": "rootless-podman",
            }))
        page = render(self.root, progress)
        visible, earlier = page.split('<details class="history">', 1)
        self.assertIn('href="runs/newer-blocked/result.json"', visible)
        self.assertIn("does not tell us whether the repair works", visible)
        self.assertNotIn('href="runs/older-success/result.json"', visible)
        self.assertIn('href="runs/older-success/result.json"', earlier)

    def test_report_does_not_present_unknown_record_as_success(self):
        path = self.root / "runs/example/result.json"
        for checks in (None, ["not a check"]):
            page = render_run(path, self.root, {
                "schema_version": 1, "candidate": "good", "outcome": "calibration_passed", "checks": checks,
            })
            self.assertIn("Unreadable record", page)
            self.assertNotIn("The proposed repair passed this check.", page)

    def test_malformed_check_fields_do_not_hide_other_runs(self):
        progress = json.loads((HERE / "progress.json").read_text())
        valid = Run(self.root)
        valid.result.update(candidate="wrong", outcome="repair_failed", checks=[
            {"id": "baseline", "outcome": "passed", "exit_code": 0},
            {"id": "seed", "outcome": "assertion_failed", "exit_code": 1},
            {"id": "candidate", "outcome": "assertion_failed", "exit_code": 1},
        ])
        runner.write_json(valid.path / "result.json", valid.result)
        for field in ("id", "outcome"):
            for value in ([], {}, None, 3):
                run = Run(self.root)
                check = {"id": "baseline", "outcome": "blocked", field: value}
                run.result.update(outcome="blocked", checks=[check])
                runner.write_json(run.path / "result.json", run.result)
        page = render(self.root, progress)
        self.assertEqual(page.count("Unreadable record"), 8)
        self.assertIn("This failure is the expected answer", page)
        self.assertIn(valid.path.name[:8], page)

    def test_missing_go_report_does_not_claim_the_repair_was_known_good(self):
        path = self.root / "runs/example/result.json"
        for candidate in ("good", "wrong", "empty", "local-patch"):
            with self.subTest(candidate=candidate):
                run = {"schema_version": 1, "candidate": candidate, "outcome": "blocked",
                       "reason": "Missing executable: go", "checks": []}
                page = render_run(path, self.root, run)
                self.assertIn("The check could not start without Go", page)
                self.assertIn("Go was unavailable", page)
                self.assertNotIn("working repair", page)
                self.assertNotIn("working-repair", page)
                self.assertIn(f"Candidate label in the saved record: {candidate}.", page)

    def test_report_requires_consistent_checks_before_reporting_success(self):
        path = self.root / "runs/example/result.json"
        checks = [{"id": name, "outcome": outcome, "exit_code": code} for name, outcome, code in (
            ("baseline", "passed", 0), ("seed", "assertion_failed", 1), ("candidate", "passed", 0))]
        run = {"schema_version": 1, "candidate": "good", "outcome": "calibration_passed", "checks": checks}
        self.assertIn("The proposed repair passed this check.", render_run(path, self.root, run))
        invalid = [[], checks[:2], checks[::-1], checks + [checks[-1]]]
        for index, field, value in ((0, "outcome", "blocked"), (1, "outcome", "passed"),
                                    (1, "exit_code", None), (1, "exit_code", 0),
                                    (2, "outcome", "build_failed"), (2, "exit_code", 1)):
            changed = [dict(check) for check in checks]
            changed[index][field] = value
            invalid.append(changed)
        for bad_checks in invalid:
            with self.subTest(checks=bad_checks):
                run["checks"] = bad_checks
                page = render_run(path, self.root, run)
                self.assertIn("Unreadable record", page)
                self.assertNotIn("The proposed repair passed this check.", page)

    def test_report_only_describes_faults_that_were_injected(self):
        path = self.root / "runs/example/result.json"
        for fault, description in (("missing-source", "Remove the importer"),
                                   ("missing-dependency", "Use an empty, temporary Go dependency cache"),
                                   ("timeout", "Run a Python command that waits")):
            with self.subTest(fault=fault):
                run = {"schema_version": 1, "candidate": "good", "fault": fault,
                       "outcome": "blocked", "reason": "Pinned base commit is unavailable", "checks": []}
                page = render_run(path, self.root, run)
                self.assertNotIn(description, page)
                self.assertIn("no record that the deliberate problem was introduced", page)
                run["fault_injected"] = fault
                self.assertIn(description, render_run(path, self.root, run))

    def test_report_does_not_judge_repairs_when_setup_fails(self):
        path = self.root / "runs/example/result.json"
        for fault, outcome, blocker, explanation in (
            ("missing-source", "blocked", "missing_source", "source file was missing"),
            ("missing-dependency", "blocked", "missing_dependency", "dependencies were unavailable"),
            ("timeout", "timeout", None, "stopped at its time limit"),
        ):
            with self.subTest(fault=fault):
                run = {"schema_version": 1, "candidate": "good", "fault": fault, "fault_injected": fault,
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
