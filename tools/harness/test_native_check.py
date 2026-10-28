import copy
import json
from pathlib import Path
import signal
import tempfile
import time
import unittest
from unittest.mock import patch

from container import ContainerRun
import native_check
from native_run import render
from native_sandbox import NativeRun
from pi_run import prepare_checkout
from run import HERE, Stop, apply_patch, digest, now, write_json
from verify import REGRESSION, REGRESSION_CHECKS, source_tree
from case_suite import build
from run import Run
from task_inputs import definition


class NativeCheckTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.checkout = self.root / "mapbox-calibration-native-check"
        self.checkout.mkdir()
        self.run = NativeRun(self.root / "native", "sha256:" + "a" * 64)
        self.run.result.update(candidate="good", verification_requested=True, tool_results=[])
        self.child_deadlines = []
        self.enterContext(patch("native_check.REPORTS", self.root))
        self.enterContext(patch("verify.preflight"))
        self.enterContext(patch.object(ContainerRun, "prepare", lambda run: setattr(run, "prepared", True)))
        self.enterContext(patch.object(ContainerRun, "execute", autospec=True, side_effect=self.execute))
        self.run.prepare()
        prepare_checkout(self.run, self.checkout)
        case = {"id": self.run.task["id"], "base_commit": self.run.task["base_commit"],
                "agent_files_sha256": source_tree(self.checkout)["files"],
                "private_files_sha256": {"verifier.go.txt": digest((HERE / "fixtures/import_verifier_test.go.txt").read_bytes())}}
        native_check.prepare_verification(self.run, case)

    def execute(self, run, name, args, cwd, build_output=False):
        run.result["containers"].append({"name": "fake-" + name, "removed": True})
        if name == "go-version":
            return {"exit_code": 0}, b"go version go1.27.1 linux/amd64\n"
        self.child_deadlines.append(run.deadline)
        source = (Path(cwd) / run.task["allowed_files"][0]).read_text()
        task = run.task.get("regression", REGRESSION) if name in REGRESSION_CHECKS else run.task
        working = ('if j > 0 {' not in source if run.task["id"] == "segment-distance-v1" else
                   'if n >= 3 && string(header[:3])' in source or (name not in REGRESSION_CHECKS and "known && n >= 3" in source))
        if run.task["id"] == "cli-inspect-v1":
            working = "index = 1" not in source
        action = "pass" if working else "fail"
        output = b"\n".join(json.dumps({"Action": action, "Test": test}).encode() for test in
                            [task["test"], *(task["test"] + "/" + case for case in task["cases"])])
        if not working:
            output += b"\n" + json.dumps({"Action": "output", "Output": task["failure_message"]}).encode()
        if name in ("candidate-format", "candidate-suite"):
            output, action = b"Required check passed", "pass"
        check = {"id": name, "command": args, "started_at": now(), "ended_at": now(), "duration_seconds": 0.01,
                 "exit_code": 0 if action == "pass" else 1, "outcome": "passed" if action == "pass" else "failed",
                 "stdout": name + ".stdout", "stderr": name + ".stderr"}
        (run.path / check["stdout"]).write_bytes(output)
        (run.path / check["stderr"]).write_bytes(b"")
        run.result["checks"].append(check)
        run.event("command_finished", check=check)
        return check, output

    def candidate(self, name="good"):
        raw = (HERE / "fixtures" / (name + ".patch")).read_bytes()
        (self.run.path / "candidate.patch").write_bytes(raw)
        apply_patch(self.run, self.checkout, raw, "candidate", ["go/import.go"])
        source = (self.checkout / "go/import.go").read_bytes()
        (self.run.path / "candidate-source.bin").write_bytes(source)
        write_json(self.run.path / "project-snapshot.json", {"files": {"go/import.go": digest(source)}})
        self.run.result.update(candidate=name, snapshot_scope_valid=True, snapshot_source_sha256=digest(source), patch_sha256=digest(raw))
        self.child_deadlines.clear()

    def distance_candidate(self, working):
        suite = Run(self.root / "distance-suite")
        manifest = build(suite, "segment-distance-v1")
        case = manifest["cases"][0]
        directory = suite.path / "cases" / case["id"]
        native_check.prepare_verification(self.run, case)
        self.assertEqual(self.run.task, definition(case["id"]))
        if working:
            patch = (directory / "private/reference.patch").read_bytes()
        else:
            patch = (HERE / "fixtures/wrong.patch").read_bytes()
        before = (directory / "agent/go/import.go").read_text()
        self.checkout.joinpath("go/import.go").write_text(before)
        self.checkout.joinpath("go/import_calibration_test.go").write_bytes((directory / "agent/go/import_calibration_test.go").read_bytes())
        (self.run.path / "candidate.patch").write_bytes(patch)
        apply_patch(self.run, self.checkout, patch, "candidate", ["go/import.go"])
        source = (self.checkout / "go/import.go").read_bytes()
        (self.run.path / "candidate-source.bin").write_bytes(source)
        write_json(self.run.path / "project-snapshot.json", {"files": {"go/import.go": digest(source)}})
        self.run.result.update(snapshot_scope_valid=True, snapshot_source_sha256=digest(source), patch_sha256=digest(patch))

    def test_distance_task_uses_its_own_visible_and_private_checks(self):
        self.distance_candidate(True)
        native_check.check_candidate(self.run)
        self.assertTrue(self.run.result["independent_checks_passed"])
        child = self.root / "verification/runs" / self.run.result["verification"]["run_id"]
        task = json.loads((child / "task.json").read_text())
        self.assertEqual(task["id"], "segment-distance-v1")
        self.assertEqual(task["regression"]["test"], "TestHarnessJourneyDistanceVerifier")
        self.assertEqual(len(self.run.result["verification"]["checks"]), 8)
        page = render(self.run.result)
        self.assertIn("TestHarnessJourneyDistance", page)
        self.assertNotIn("TestXMLImportsAcceptLeadingUTF8BOM", page)
        self.assertNotIn("XML-prefix", page)

    def cli_candidate(self):
        suite = Run(self.root / "cli-suite")
        case = build(suite, "cli-inspect-v1")["cases"][0]
        directory = suite.path / "cases" / case["id"]
        native_check.prepare_verification(self.run, case)
        for name in ("go/cli.go", "go/import_calibration_test.go"):
            (self.checkout / name).write_bytes((directory / "agent" / name).read_bytes())
        patch = (directory / "private/reference.patch").read_bytes()
        (self.run.path / "candidate.patch").write_bytes(patch)
        apply_patch(self.run, self.checkout, patch, "candidate", ["go/cli.go"])
        source = (self.checkout / "go/cli.go").read_bytes()
        (self.run.path / "candidate-source.bin").write_bytes(source)
        write_json(self.run.path / "project-snapshot.json", {"files": {"go/cli.go": digest(source)}})
        self.run.result.update(snapshot_scope_valid=True, snapshot_source_sha256=digest(source), patch_sha256=digest(patch))

    def test_cli_candidate_reconstructs_the_correct_source_in_separate_checks(self):
        self.cli_candidate()
        native_check.check_candidate(self.run)
        self.assertTrue(self.run.result["independent_checks_passed"])
        self.assertEqual(len(self.run.result["verification"]["checks"]), 8)

    def test_cli_snapshot_cannot_substitute_the_import_source(self):
        self.cli_candidate()
        raw = (self.run.path / "candidate-source.bin").read_bytes()
        write_json(self.run.path / "project-snapshot.json", {"files": {"go/import.go": digest(raw)}})
        with self.assertRaises(Stop):
            native_check.check_candidate(self.run)
        self.assertFalse(self.run.result["independent_checks_passed"])

    def test_completed_checks_cannot_promote_a_patch_after_the_shared_deadline(self):
        self.candidate()
        original = native_check.verification_matches
        def expired(parent, child):
            matched = original(parent, child)
            parent.deadline = time.monotonic() - 1
            return matched
        with patch("native_check.verification_matches", side_effect=expired), self.assertRaises(Stop) as caught:
            native_check.check_candidate(self.run)
        self.assertEqual(caught.exception.outcome, "timeout")
        self.assertFalse(self.run.result["independent_checks_passed"])
        self.assertEqual(self.run.result["verification"]["outcome"], "repository_verification_passed")

    def test_distance_bad_fix_stays_failed_and_never_reaches_repository_checks(self):
        self.distance_candidate(False)
        with self.assertRaises(Stop) as caught:
            native_check.check_candidate(self.run)
        self.assertEqual(caught.exception.outcome, "repair_failed")
        self.assertFalse(self.run.result["independent_checks_passed"])
        self.assertEqual([c["id"] for c in self.run.result["verification"]["checks"]], ["baseline", "seed", "candidate"])

    def test_saved_fix_passes_only_after_every_check_and_keeps_shared_deadline(self):
        self.candidate()
        native_check.check_candidate(self.run)
        self.assertTrue(self.run.result["independent_checks_passed"])
        self.assertEqual(self.run.result["outcome"], "native_verification_passed")
        self.assertEqual(len(self.run.result["verification"]["checks"]), 8)
        self.assertEqual(set(self.child_deadlines), {self.run.deadline})
        self.assertIn("saved fix passed the separate checker", render(self.run.result))
        with self.assertRaises(Stop) as caught:
            native_check.check_candidate(self.run)
        self.assertEqual(caught.exception.outcome, "budget_exhausted")
        self.assertFalse(self.run.result["independent_checks_passed"])

    def test_filename_shortcut_passes_visible_test_and_fails_extra_examples(self):
        self.candidate("tailored")
        with self.assertRaises(Stop) as caught:
            native_check.check_candidate(self.run)
        self.assertEqual(caught.exception.outcome, "repair_failed")
        self.assertFalse(self.run.result["independent_checks_passed"])
        checks = {row["id"]: row["outcome"] for row in self.run.result["verification"]["checks"]}
        self.assertEqual(checks["candidate"], "passed")
        self.assertEqual(checks["candidate-regression"], "assertion_failed")
        self.assertNotIn("candidate-suite", checks)

    def test_changed_patch_source_snapshot_or_cleanup_stops_before_checker(self):
        self.candidate()
        saved = copy.deepcopy(self.run.result)
        files = {name: (self.run.path / name).read_bytes() for name in
                 ("candidate.patch", "candidate-source.bin", "project-snapshot.json")}
        for change in (*files, "cleanup", "scope"):
            self.run.result = copy.deepcopy(saved)
            for name, raw in files.items():
                (self.run.path / name).write_bytes(raw)
            if change in files:
                (self.run.path / change).write_bytes(b'{}' if change.endswith("json") else b"changed")
            elif change == "cleanup":
                self.run.result["containers"][0]["removed"] = False
            else:
                self.run.result["snapshot_scope_valid"] = False
            with self.subTest(change=change), patch("native_check.VerificationRun") as child, self.assertRaises(Stop):
                native_check.check_candidate(self.run)
            child.assert_not_called()
            self.assertFalse(self.run.result["independent_checks_passed"])

    def test_deadline_and_cancellation_do_not_create_a_new_budget(self):
        self.candidate()
        self.run.deadline = time.monotonic() - 1
        with self.assertRaises(Stop) as caught:
            native_check.check_candidate(self.run)
        self.assertEqual(caught.exception.outcome, "timeout")
        self.assertNotIn("verification", self.run.result)
        self.run.cancel_requested = True
        with self.assertRaises(KeyboardInterrupt):
            native_check.check_candidate(self.run)

    def test_changed_rules_stop_before_checking(self):
        self.candidate()
        with patch("native_check.check_evaluator", side_effect=Stop("invalid_input", "Rules changed")), self.assertRaises(Stop):
            native_check.check_candidate(self.run)
        self.assertNotIn("verification", self.run.result)

    def test_interrupted_checker_saves_its_result_and_restores_handlers(self):
        self.candidate()
        before = {number: signal.getsignal(number) for number in (signal.SIGINT, signal.SIGTERM)}
        with patch("native_check.verify", side_effect=KeyboardInterrupt), self.assertRaises(KeyboardInterrupt):
            native_check.check_candidate(self.run)
        self.assertEqual(self.run.result["verification"]["outcome"], "cancelled")
        self.assertFalse(self.run.result["independent_checks_passed"])
        self.assertIn("separate checker", render(self.run.result))
        self.assertEqual(before, {number: signal.getsignal(number) for number in before})

    def test_missing_evidence_or_unconfirmed_cleanup_cannot_be_promoted(self):
        self.candidate()
        original = native_check.verify
        for change in ("log", "source", "cleanup", "seed", "private-test"):
            self.run.result.pop("verification", None)

            def changed(child, raw):
                original(child, raw)
                if change == "log":
                    (child.path / "candidate.stdout").write_bytes(b"changed")
                elif change == "source":
                    child.result["trees"]["candidate"]["files"]["go/import.go"] = "other source"
                elif change == "cleanup":
                    child.result["containers"][0]["removed"] = False
                elif change == "seed":
                    child.result["trees"]["seed"]["sha256"] = "other project"
                else:
                    child.result["regression_sha256"] = "other private examples"

            with self.subTest(change=change), patch("native_check.verify", side_effect=changed), self.assertRaises(Stop) as caught:
                native_check.check_candidate(self.run)
            self.assertEqual(caught.exception.outcome, "invalid_input")
            self.assertFalse(self.run.result["independent_checks_passed"])

    def test_patch_or_source_changed_during_checking_cannot_be_promoted(self):
        self.candidate()
        original = native_check.verify
        for name in ("candidate.patch", "candidate-source.bin"):
            path = self.run.path / name
            saved = path.read_bytes()
            self.run.result.pop("verification", None)

            def changed(child, raw):
                original(child, raw)
                path.write_bytes(b"changed during checking")

            with self.subTest(file=name), patch("native_check.verify", side_effect=changed), self.assertRaises(Stop) as caught:
                native_check.check_candidate(self.run)
            self.assertEqual(caught.exception.outcome, "invalid_input")
            self.assertFalse(self.run.result["independent_checks_passed"])
            path.write_bytes(saved)

    def test_changed_published_receipt_cannot_be_promoted(self):
        self.candidate()
        original = self.run.event

        def changed(kind, **fields):
            original(kind, **fields)
            if kind == "verification_finished":
                path = self.root / "verification/runs" / fields["verification"]["run_id"] / "result.json"
                path.write_text('{"outcome":"repository_verification_passed"}')

        with patch.object(self.run, "event", side_effect=changed), self.assertRaises(Stop) as caught:
            native_check.check_candidate(self.run)
        self.assertEqual(caught.exception.outcome, "invalid_input")
        self.assertFalse(self.run.result["independent_checks_passed"])


if __name__ == "__main__":
    unittest.main()
