import copy
import json
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import report
import verify
from run import HERE, Run, Stop, digest, now
from report import render


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.run = verify.VerificationRun(self.root / "verification", "sha256:" + "a" * 64)

    def response(self, name, args, cwd):
        if name == "go-version":
            return {"exit_code": 0}, b"go version go1.27.1 linux/amd64\n"
        task = self.run.task
        outcome = "fail" if name == "seed" or name == "candidate" and self.wrong else "pass"
        events = [{"Action": outcome, "Test": test} for test in
                  [task["test"], *(task["test"] + "/" + case for case in task["cases"])]]
        if outcome == "fail":
            events.append({"Action": "output", "Output": task["failure_message"]})
        output = b"\n".join(json.dumps(event).encode() for event in events)
        check = {"id": name, "command": args, "started_at": now(), "ended_at": now(),
                 "duration_seconds": 0.01, "exit_code": 1 if outcome == "fail" else 0,
                 "outcome": "failed" if outcome == "fail" else "passed",
                 "stdout": name + ".stdout", "stderr": name + ".stderr"}
        (self.run.path / check["stdout"]).write_bytes(output)
        (self.run.path / check["stderr"]).write_bytes(b"")
        self.run.result["checks"].append(check)
        self.run.event("command_finished", check=check)
        return check, output

    def verify_fixture(self, demo="good"):
        self.wrong = demo == "wrong"
        self.run.result["candidate"] = demo
        with patch("verify.preflight"), patch.object(self.run, "prepare", side_effect=lambda: setattr(self.run, "prepared", True)), \
                patch.object(self.run, "execute", side_effect=self.response):
            verify.verify(self.run, (HERE / "fixtures" / f"{demo}.patch").read_bytes())

    def test_frozen_patch_checked_in_fresh_copy_and_bound_to_each_measurement(self):
        self.verify_fixture()
        result = self.run.result
        self.assertEqual(result["outcome"], "focused_verification_passed")
        self.assertFalse(result["fully_verified"])
        self.assertIsNone(result["model_claim"])
        self.assertEqual(result["patch_sha256"], digest((HERE / "fixtures/good.patch").read_bytes()))
        self.assertTrue(verify.receipt_matches(result, self.run.path))
        stages = [check for check in result["checks"] if check["id"] in verify.STAGES]
        self.assertEqual(len({check["definition_sha256"] for check in stages}), 1)
        for check in result["checks"]:
            self.assertEqual(check["patch_sha256"], result["patch_sha256"])
            self.assertEqual(check["run_id"], result["run_id"])
            self.assertEqual(check["attempt"], 1)
            self.assertEqual(check["base_commit"], self.run.task["base_commit"])
        events = [json.loads(line) for line in (self.run.path / "events.jsonl").read_text().splitlines()]
        self.assertEqual(events[0]["kind"], "candidate_frozen")
        self.assertTrue(all(event["patch_sha256"] == result["patch_sha256"] for event in events))
        page = render(self.root, {**json.loads((HERE / "progress.json").read_text()), "stage": "Verify"})
        self.assertIn("independent patch checks", page)
        self.assertIn("This frozen patch passed", page)
        self.assertIn(result["patch_sha256"], page)

    def test_wrong_repair_is_a_failed_check_with_its_own_evidence(self):
        with self.assertRaises(Stop) as caught:
            self.verify_fixture("wrong")
        self.assertEqual(caught.exception.outcome, "repair_failed")
        check = next(check for check in self.run.result["checks"] if check["id"] == "candidate")
        self.assertEqual(check["outcome"], "assertion_failed")
        self.assertEqual(check["exit_code"], 1)
        self.assertIn("stdout_sha256", check)
        self.assertFalse(verify.receipt_matches(self.run.result, self.run.path))

    def test_empty_and_oversized_repairs_do_not_start_sandbox(self):
        for candidate in (b"", b" \n", b"x" * (256 * 1024 + 1)):
            with self.subTest(size=len(candidate)), patch("verify.preflight") as preflight, self.assertRaises(Stop):
                verify.verify(self.run, candidate)
            preflight.assert_not_called()

    def test_patches_to_tests_and_new_unapproved_files_are_rejected(self):
        for target in ("go/import_edge_test.go", "Makefile", "go/new.go", "../escape.go"):
            self.run = verify.VerificationRun(self.root / "verification", "sha256:" + "a" * 64)
            self.wrong = False
            candidate = (HERE / "fixtures/good.patch").read_bytes().replace(b"go/import.go", target.encode())
            with self.subTest(target=target), patch("verify.preflight"), \
                    patch.object(self.run, "prepare", side_effect=lambda: setattr(self.run, "prepared", True)), \
                    patch.object(self.run, "execute", side_effect=self.response), self.assertRaises(Stop) as caught:
                verify.verify(self.run, candidate)
            self.assertEqual(caught.exception.outcome, "invalid_input")
            self.assertFalse(any(check["id"] == "candidate" for check in self.run.result["checks"]))

    def test_stale_missing_and_fabricated_receipts_cannot_pass(self):
        self.verify_fixture()
        original = copy.deepcopy(self.run.result)
        for mutate in (
            lambda result: result.update(patch_sha256="earlier-patch"),
            lambda result: result.update(run_id="earlier-run"),
            lambda result: result["checks"].pop(),
            lambda result: result["trees"]["candidate"]["files"].update({"Makefile": "changed"}),
            lambda result: next(check for check in result["checks"] if check["id"] == "candidate").update(
                patch_sha256="earlier-patch"),
            lambda result: next(check for check in result["checks"] if check["id"] == "candidate").update(
                logical_command=["echo", "PASS"]),
        ):
            result = copy.deepcopy(original)
            mutate(result)
            self.assertFalse(verify.receipt_matches(result, self.run.path))
        for name in ("candidate.patch", "task.json", "seed.patch", "candidate.stdout", "candidate.stderr"):
            path = self.run.path / name
            saved = path.read_bytes()
            path.write_text("done\nPASS\n")
            self.assertFalse(verify.receipt_matches(original, self.run.path), name)
            path.write_bytes(saved)
        check = next(check for check in original["checks"] if check["id"] == "candidate")
        (self.run.path / "candidate.stdout").write_text("done\nPASS\n")
        check["stdout_sha256"] = digest(b"done\nPASS\n")
        self.assertFalse(verify.receipt_matches(original, self.run.path))

    def test_linked_source_is_rejected(self):
        checkout = self.root / "copy"
        checkout.mkdir()
        (checkout / "linked").symlink_to(HERE / "task.json")
        with self.assertRaises(Stop):
            verify.source_tree(checkout)

    def test_source_changes_during_check_invalidate_the_attempt(self):
        self.wrong = False

        def mutate_source(name, args, cwd):
            response = self.response(name, args, cwd)
            if name == "candidate":
                (Path(cwd) / "Makefile").write_text("changed while checking")
            return response

        with patch("verify.preflight"), \
                patch.object(self.run, "prepare", side_effect=lambda: setattr(self.run, "prepared", True)), \
                patch.object(self.run, "execute", side_effect=mutate_source), self.assertRaises(Stop) as caught:
            verify.verify(self.run, (HERE / "fixtures/good.patch").read_bytes())
        self.assertEqual(caught.exception.outcome, "invalid_input")

    def test_saved_verify_report_preserves_logs_and_rechecks_patch_identity(self):
        self.verify_fixture()
        progress = {**json.loads((HERE / "progress.json").read_text()), "stage": "Verify"}
        saved = self.root / "saved"
        report.save_stage(saved, progress, self.root, source_ref="a" * 40)
        page = report.render_saved(saved, "verify").read_text()
        self.assertIn("This frozen patch passed", page)
        copied = saved / "snapshots/verify/verification/runs" / self.run.path.name
        self.assertEqual((copied / "candidate.stdout").read_bytes(), (self.run.path / "candidate.stdout").read_bytes())
        (self.run.path / "candidate.patch").write_text("later patch")
        page = report.render(self.root, progress)
        self.assertIn("Evidence needs inspection", page)
        self.assertNotIn('<p>This frozen patch passed', page)

    def test_environment_failure_and_cancellation_keep_incomplete_check_measurements(self):
        self.run.result.update(patch_sha256="frozen", base_commit="base", task_sha256="task")
        for name, args, deadline, expected in (
            ("missing", [str(self.root / "missing-command")], 5, "blocked"),
            ("timeout", [sys.executable, "-c", "import time; time.sleep(10)"], 0.1, "timeout"),
        ):
            self.run.deadline = time.monotonic() + deadline
            with self.subTest(name=name), self.assertRaises(Stop) as caught:
                Run.command(self.run, name, args, self.root)
            self.assertEqual(caught.exception.outcome, expected)
            check = self.run.result["checks"][-1]
            self.assertEqual(check["outcome"], expected)
            self.assertEqual(check["patch_sha256"], "frozen")
            self.assertIn("ended_at", check)
            self.assertIn("duration_seconds", check)

    def test_entry_point_finalizes_all_stop_outcomes_without_model_work(self):
        for outcome in ("repair_failed", "blocked", "timeout", "cancelled", "incomplete"):
            failure = KeyboardInterrupt() if outcome == "cancelled" else Stop(outcome, "Expected failure")
            with self.subTest(outcome=outcome), patch("verify.REPORTS", self.root), \
                    patch.object(verify, "VerificationRun", return_value=self.run), \
                    patch("verify.verify", side_effect=failure), \
                    patch.object(sys, "argv", ["verify.py", "--image", self.run.image, "--demo", "good"]):
                self.assertEqual(verify.main(), 1)
            result = json.loads((self.run.path / "result.json").read_text())
            self.assertEqual(result["outcome"], outcome)
            self.assertIn("ended_at", result)
            self.assertEqual(result["human_acceptance"], "pending")


if __name__ == "__main__":
    unittest.main()
