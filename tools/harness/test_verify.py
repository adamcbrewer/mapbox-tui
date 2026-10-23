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

    def response(self, name, args, cwd, build_output=False):
        self.assertEqual(build_output, name == "candidate-suite")
        if name == "go-version":
            return {"exit_code": 0}, b"go version go1.27.1 linux/amd64\n"
        task = verify.REGRESSION if name in verify.REGRESSION_CHECKS else self.run.task
        failed = name in ("seed", "seed-regression") or name == "candidate" and self.wrong
        failed = failed or name == "candidate-regression" and getattr(self, "tailored", False)
        outcome = "fail" if failed else "pass"
        events = [{"Action": outcome, "Test": test} for test in
                  [task["test"], *(task["test"] + "/" + case for case in task["cases"])]]
        if outcome == "fail":
            events.append({"Action": "output", "Output": task["failure_message"]})
        output = b"\n".join(json.dumps(event).encode() for event in events)
        if name in verify.EXTRA_CHECKS:
            unformatted = name == "candidate-format" and b"        if n >= 3" in (Path(cwd) / "go/import.go").read_bytes()
            outcome = "fail" if unformatted or name == getattr(self, "extra_failure", None) else "pass"
            output = b"Required check failed" if outcome == "fail" else b"Required check completed"
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
        self.tailored = demo == "tailored"
        self.run.result["candidate"] = demo
        with patch("verify.preflight"), patch.object(self.run, "prepare", side_effect=lambda: setattr(self.run, "prepared", True)), \
                patch.object(self.run, "execute", side_effect=self.response):
            verify.verify(self.run, (HERE / "fixtures" / f"{demo}.patch").read_bytes())

    def test_frozen_patch_checked_in_fresh_copy_and_bound_to_each_measurement(self):
        self.verify_fixture()
        result = self.run.result
        self.assertEqual(result["outcome"], "repository_verification_passed")
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
        self.assertIn('<strong>Go formatting:</strong> passed', page)
        self.assertIn('<strong>Repository make test:</strong> passed', page)
        self.assertIn('<strong>Extra cases on original code:</strong> passed', page)
        self.assertIn('<strong>Extra cases on broken code:</strong> failed as expected', page)
        self.assertIn('<strong>Extra cases on repaired code:</strong> passed', page)

    def test_filename_shortcut_passes_original_test_but_fails_independent_cases(self):
        with self.assertRaises(Stop) as caught:
            self.verify_fixture("tailored")
        self.assertEqual(caught.exception.outcome, "repair_failed")
        checks = {check["id"]: check for check in self.run.result["checks"]}
        self.assertEqual(checks["candidate"]["outcome"], "passed")
        self.assertEqual(checks["baseline-regression"]["outcome"], "passed")
        self.assertEqual(checks["seed-regression"]["outcome"], "assertion_failed")
        self.assertEqual(checks["candidate-regression"]["outcome"], "assertion_failed")
        self.assertNotIn("candidate-format", checks)
        self.assertTrue(verify.receipt_matches(self.run.result, self.run.path, allow_failed=True))
        self.assertFalse(verify.receipt_matches(self.run.result, self.run.path))
        self.run.result.update(outcome="repair_failed")
        page = report.render_run(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn('This failure is the expected answer', page)
        self.assertIn('<strong>Extra cases on repaired code:</strong> failed', page)
        self.assertIn('<strong>Repository make test:</strong> not run', page)

    def test_permitted_helper_is_frozen_checked_and_absent_from_unfixed_copies(self):
        self.verify_fixture("new-file")
        result = self.run.result
        self.assertTrue(verify.receipt_matches(result, self.run.path))
        self.assertFalse(list(self.run.path.rglob("*.go")))
        self.assertEqual(set(result["file_changes"]), {"go/import.go", "go/import_bom.go"})
        added = result["file_changes"]["go/import_bom.go"]
        self.assertIsNone(added["before_sha256"])
        self.assertEqual(added["after_sha256"], result["trees"]["candidate"]["files"]["go/import_bom.go"])
        for stage in ("baseline", "seed", "baseline-regression", "seed-regression"):
            self.assertNotIn("go/import_bom.go", result["trees"][stage]["files"])
        self.assertIn("go/import_bom.go", result["trees"]["candidate-regression"]["files"])
        page = report.render_run(self.run.path / "result.json", self.root, result)
        self.assertIn("repair with a permitted new file", page)
        self.assertIn("go/import_bom.go</code>: added", page)
        self.assertIn('<strong>Repository make test:</strong> passed', page)

    def test_unapproved_new_test_is_rejected_before_candidate_execution(self):
        with self.assertRaises(Stop) as caught:
            self.verify_fixture("forbidden-file")
        self.assertEqual(caught.exception.outcome, "invalid_input")
        self.assertEqual(self.run.result["rejected_paths"], ["go/import_verifier_test.go"])
        self.assertFalse(any(check["id"] == "candidate" for check in self.run.result["checks"]))
        self.run.result.update(outcome="invalid_input")
        page = report.render_run(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("unapproved test file refused", page)
        self.assertIn("before any proposed-repair code ran", page)

    def test_new_helper_requires_explicit_permission(self):
        self.run.task["allowed_new_files"] = []
        verify.freeze_evaluator(self.run)
        with self.assertRaises(Stop) as caught:
            self.verify_fixture("new-file")
        self.assertEqual(caught.exception.outcome, "invalid_input")
        self.assertEqual(self.run.result["rejected_paths"], ["go/import_bom.go"])

    def test_invalid_new_file_permissions_stop_before_sandbox(self):
        for names in (None, "go/helper.go", ["../helper.go"], ["go/../helper.go"],
                      ["go/helper_test.go"], ["Makefile"], ["go.mod"], ["go/import.go"],
                      ["go/helper.go", "go/helper.go"], ["go/sub/helper.go"], [{}]):
            self.run.task["allowed_new_files"] = names
            verify.freeze_evaluator(self.run)
            with self.subTest(names=names), patch("verify.preflight") as preflight, self.assertRaises(Stop) as caught:
                verify.verify(self.run, (HERE / "fixtures/new-file.patch").read_bytes())
            self.assertEqual(caught.exception.outcome, "invalid_input")
            preflight.assert_not_called()

    def test_new_source_manifest_must_match_all_changed_file_fingerprints(self):
        self.verify_fixture("new-file")
        for mutation in (
            lambda result: result["file_changes"].pop("go/import_bom.go"),
            lambda result: result["file_changes"]["go/import_bom.go"].update(after_sha256="earlier-helper"),
            lambda result: result["file_changes"]["go/import_bom.go"].update(before_sha256="pretend-existing"),
        ):
            result = copy.deepcopy(self.run.result)
            mutation(result)
            self.assertFalse(verify.receipt_matches(result, self.run.path))

    def test_earlier_independent_scope_keeps_its_existing_file_policy(self):
        self.verify_fixture()
        result = copy.deepcopy(self.run.result)
        result.update(verification_scope="independent-cases-v1", suite_version=3)
        result.pop("file_changes")
        for check in result["checks"]:
            check.update(verification_scope="independent-cases-v1", suite_version=3)
        self.assertTrue(verify.receipt_matches(result, self.run.path))
        evidence = self.run.path / "import_verifier_test.go.txt"
        legacy = evidence.with_suffix("")
        evidence.rename(legacy)
        self.assertTrue(verify.receipt_matches(result, self.run.path))
        legacy.rename(evidence)
        result.update(verification_scope=verify.SCOPE, suite_version=verify.SUITE_VERSION)
        for check in result["checks"]:
            check.update(verification_scope=verify.SCOPE, suite_version=verify.SUITE_VERSION)
        self.assertFalse(verify.receipt_matches(result, self.run.path))

    def test_independent_cases_must_expose_the_bug_before_judging_the_repair(self):
        response = self.response
        self.wrong = False

        def insensitive_test(name, args, cwd, build_output=False):
            check, output = response(name, args, cwd, build_output)
            if name == "seed-regression":
                output = output.replace(b'"fail"', b'"pass"')
                check.update(exit_code=0, outcome="passed")
                (self.run.path / check["stdout"]).write_bytes(output)
            return check, output

        with patch("verify.preflight"), \
                patch.object(self.run, "prepare", side_effect=lambda: setattr(self.run, "prepared", True)), \
                patch.object(self.run, "execute", side_effect=insensitive_test), self.assertRaises(Stop) as caught:
            verify.verify(self.run, (HERE / "fixtures/good.patch").read_bytes())
        self.assertEqual(caught.exception.outcome, "calibration_failed")
        self.assertFalse(any(check["id"] == "candidate-regression" for check in self.run.result["checks"]))

    def test_missing_cases_changed_trusted_test_or_different_source_cannot_pass(self):
        self.verify_fixture()
        original = copy.deepcopy(self.run.result)
        for name in verify.REGRESSION_CHECKS:
            result = copy.deepcopy(original)
            result["checks"] = [check for check in result["checks"] if check["id"] != name]
            self.assertFalse(verify.receipt_matches(result, self.run.path))
            result = copy.deepcopy(original)
            tree = result["trees"][name]
            tree["files"]["go/import.go"] = original["trees"]["seed"]["files"]["go/import.go"] + "different"
            tree["sha256"] = verify.fingerprint(tree["files"])
            next(check for check in result["checks"] if check["id"] == name)["tree_sha256"] = tree["sha256"]
            self.assertFalse(verify.receipt_matches(result, self.run.path))
        (self.run.path / "import_verifier_test.go.txt").write_text("package main\n")
        self.assertFalse(verify.receipt_matches(original, self.run.path))

    def test_earlier_repository_scope_cannot_claim_extra_case_evidence(self):
        self.verify_fixture()
        result = copy.deepcopy(self.run.result)
        result.update(verification_scope="repository-checks-v1", suite_version=2,
                      required_checks=list(verify.STAGES + verify.EXTRA_CHECKS))
        result["checks"] = [check for check in result["checks"] if check["id"] not in verify.REGRESSION_CHECKS]
        for check in result["checks"]:
            check.update(verification_scope="repository-checks-v1", suite_version=2)
        self.assertTrue(verify.receipt_matches(result, self.run.path))
        page = report.render_run(self.run.path / "result.json", self.root, result)
        self.assertIn("without extra independent cases", page)
        self.assertNotIn('<strong>Extra cases on repaired code:</strong> passed', page)
        result.update(verification_scope=verify.SCOPE, suite_version=3, required_checks=list(verify.REQUIRED_CHECKS))
        self.assertFalse(verify.receipt_matches(result, self.run.path))

    def test_wrong_repair_is_a_failed_check_with_its_own_evidence(self):
        with self.assertRaises(Stop) as caught:
            self.verify_fixture("wrong")
        self.assertEqual(caught.exception.outcome, "repair_failed")
        check = next(check for check in self.run.result["checks"] if check["id"] == "candidate")
        self.assertEqual(check["outcome"], "assertion_failed")
        self.assertEqual(check["exit_code"], 1)
        self.assertIn("stdout_sha256", check)
        self.assertFalse(verify.receipt_matches(self.run.result, self.run.path))

    def test_passing_import_test_does_not_override_format_or_suite_failure(self):
        for demo, failure in (("unformatted", "candidate-format"), ("good", "candidate-suite")):
            self.run = verify.VerificationRun(self.root / "verification", "sha256:" + "a" * 64)
            self.extra_failure = failure
            with self.subTest(failure=failure), self.assertRaises(Stop) as caught:
                self.verify_fixture(demo)
            self.assertEqual(caught.exception.outcome, "repair_failed")
            checks = [check for check in self.run.result["checks"] if check["id"] in verify.STAGES + verify.EXTRA_CHECKS]
            self.assertEqual(checks[2]["outcome"], "passed")
            self.assertEqual(checks[-1]["id"], failure)
            self.assertEqual(checks[-1]["exit_code"], 1)
            self.assertTrue(verify.receipt_matches(self.run.result, self.run.path, allow_failed=True))
            self.assertFalse(verify.receipt_matches(self.run.result, self.run.path))
            self.run.result.update(outcome="repair_failed")
            page = report.render_run(self.run.path / "result.json", self.root, self.run.result)
            self.assertIn('<strong>Go formatting:</strong> ' + ('failed' if demo == 'unformatted' else 'passed'), page)
            if demo == "unformatted":
                self.assertIn('<strong>Repository make test:</strong> not run', page)
                self.assertIn('This failure is the expected answer', page)

    def test_missing_broader_check_or_different_candidate_cannot_satisfy_gate(self):
        self.verify_fixture()
        for name in verify.EXTRA_CHECKS:
            result = copy.deepcopy(self.run.result)
            result["checks"] = [check for check in result["checks"] if check["id"] != name]
            self.assertFalse(verify.receipt_matches(result, self.run.path))
            result = copy.deepcopy(self.run.result)
            next(check for check in result["checks"] if check["id"] == name)["tree_sha256"] = "earlier-candidate"
            self.assertFalse(verify.receipt_matches(result, self.run.path))

    def test_earlier_focused_receipts_retain_their_smaller_scope(self):
        self.verify_fixture()
        result = copy.deepcopy(self.run.result)
        result.update(verification_scope="focused-only-v1", suite_version=1,
                      required_checks=list(verify.STAGES), outcome="focused_verification_passed")
        result["checks"] = [check for check in result["checks"] if check["id"] in verify.STAGES or check["id"] not in verify.REQUIRED_CHECKS]
        for check in result["checks"]:
            check.update(verification_scope="focused-only-v1", suite_version=1)
        self.assertTrue(verify.receipt_matches(result, self.run.path))
        page = report.render_run(self.run.path / "result.json", self.root, result)
        self.assertIn('passed the focused check', page)
        self.assertNotIn('<strong>Repository make test:</strong> passed', page)
        result["outcome"] = "repository_verification_passed"
        page = report.render_run(self.run.path / "result.json", self.root, result)
        self.assertIn('Evidence needs inspection', page)

    def test_missing_dependency_in_broader_check_stays_an_environment_failure(self):
        response = self.response
        self.wrong = False

        def missing_dependency(name, args, cwd, build_output=False):
            check, output = response(name, args, cwd, build_output)
            if name == "candidate-suite":
                output = b"module lookup disabled by GOPROXY=off"
                check.update(exit_code=2, outcome="failed")
                (self.run.path / check["stdout"]).write_bytes(output)
            return check, output

        with patch("verify.preflight"), \
                patch.object(self.run, "prepare", side_effect=lambda: setattr(self.run, "prepared", True)), \
                patch.object(self.run, "execute", side_effect=missing_dependency), self.assertRaises(Stop) as caught:
            verify.verify(self.run, (HERE / "fixtures/good.patch").read_bytes())
        self.assertEqual(caught.exception.outcome, "blocked")
        self.assertEqual(self.run.result["checks"][-1]["outcome"], "blocked")

    def test_arbitrary_broader_commands_and_environment_changes_are_rejected(self):
        with patch.object(self.run, "execute") as execute:
            for name, args, overrides in (("candidate-suite", ["make", "install"], None),
                                          ("candidate-format", ["python3", "other.py"], None),
                                          ("candidate-suite", verify.check_commands(self.run.task)["candidate-suite"], {"PATH": "/host"})):
                with self.subTest(name=name, args=args), self.assertRaises(Stop) as caught:
                    self.run.command(name, args, self.root, overrides)
                self.assertEqual(caught.exception.outcome, "invalid_input")
            execute.assert_not_called()

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

    def test_frozen_rules_are_required_in_the_receipt_and_each_check(self):
        self.verify_fixture()
        original = copy.deepcopy(self.run.result)
        for mutate in (
            lambda result: result.pop("evaluator"),
            lambda result: result.pop("evaluator_sha256"),
            lambda result: result["evaluator"].update(regression_sha256="different-tests"),
            lambda result: result["checks"][-1].update(evaluator_sha256="different-rules"),
        ):
            result = copy.deepcopy(original)
            mutate(result)
            self.assertFalse(verify.receipt_matches(result, self.run.path))

    def test_rules_changing_during_a_check_invalidate_the_attempt(self):
        response = self.response
        read_bytes = Path.read_bytes
        changed = False

        def changed_source(path):
            content = read_bytes(path)
            if changed and path == HERE / "container.py":
                return content + b"\n# Changed runner\n"
            return content

        def change_during_check(name, args, cwd, build_output=False):
            nonlocal changed
            result = response(name, args, cwd, build_output)
            changed = name == "candidate"
            return result

        with patch.object(self, "response", side_effect=change_during_check), \
                patch.object(Path, "read_bytes", changed_source), self.assertRaises(Stop) as caught:
            self.verify_fixture()
        self.assertEqual(caught.exception.outcome, "invalid_input")
        self.assertIn("Verification rules changed", caught.exception.reason)

    def test_linked_source_is_rejected(self):
        checkout = self.root / "copy"
        checkout.mkdir()
        (checkout / "linked").symlink_to(HERE / "task.json")
        with self.assertRaises(Stop):
            verify.source_tree(checkout)

    def test_source_changes_during_check_invalidate_the_attempt(self):
        self.wrong = False

        def mutate_source(name, args, cwd, build_output=False):
            response = self.response(name, args, cwd, build_output)
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
