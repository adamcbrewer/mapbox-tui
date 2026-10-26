import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import case_suite
from container import ContainerRun
from run import Run, Stop, now


class CaseSuiteTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.run = Run(self.root / "case-suites")
        self.manifest = case_suite.build(self.run)
        self.enterContext(patch("case_suite.REPORTS", self.root))
        self.enterContext(patch("case_suite.preflight"))
        self.enterContext(patch.object(ContainerRun, "prepare", lambda run: setattr(run, "prepared", True)))
        self.execute_mock = self.enterContext(patch.object(ContainerRun, "execute", autospec=True, side_effect=self.execute))

    def execute(self, run, name, args, cwd, build_output=False):
        if name == "go-version":
            return {"exit_code": 0}, b"go version go1.27.1 linux/amd64\n"
        case = next(case for case in self.manifest["cases"] if case["id"] == run.result["case"])
        visibility = name.rsplit("-", 1)[1]
        source = (Path(cwd) / case_suite.SOURCE).read_text()
        broken = ('if n >= 3 && string(header[:3])' not in source if case["partition"] == "development" else
                  'math.Abs(p.Lat) > 900' in source)
        action = "fail" if broken else "pass"
        test = case[visibility + "_test"]
        output = b"\n".join(json.dumps({"Action": action, "Test": identity}).encode() for identity in
                            [test, *(test + "/" + subtest for subtest in case[visibility + "_cases"])])
        if broken:
            message = case_suite.FAILURE_MESSAGES[case["partition"]][visibility]
            output += b"\n" + json.dumps({"Action": "output", "Output": message}).encode()
        check = {"id": name, "command": args, "exit_code": int(broken), "outcome": action,
                 "started_at": now(), "stdout": name + ".stdout", "stderr": name + ".stderr"}
        (run.path / check["stdout"]).write_bytes(output)
        (run.path / check["stderr"]).write_bytes(b"")
        run.result["checks"].append(check)
        return check, output

    def test_suite_has_distinct_development_and_held_out_families(self):
        manifest = case_suite.load_suite(self.run.path)
        self.assertEqual([case["partition"] for case in manifest["cases"]], ["development", "held-out"])
        self.assertNotEqual(manifest["cases"][0]["family"], manifest["cases"][1]["family"])
        self.assertEqual(manifest["model_calls"], 0)

    def test_only_public_task_and_seeded_source_enter_agent_projects(self):
        for case in self.manifest["cases"]:
            directory = self.run.path / "cases" / case["id"]
            agent = directory / "agent"
            case_suite.validate_case(directory, case)
            public = json.loads((agent / "task.json").read_text())
            self.assertEqual(set(public), {"schema_version", "id", "synthetic", "expected_behaviour", "allowed_files", "command"})
            self.assertFalse((agent / case_suite.PRIVATE_FILE).exists())
            self.assertFalse((agent / ".git").exists())
            self.assertFalse(list(agent.rglob("*.patch")))
            self.assertNotEqual((agent / case_suite.SOURCE).read_bytes(), (directory / "private/original-import.go.txt").read_bytes())
            self.assertTrue((directory / "private/reference.patch").is_file())

    def test_case_calibration_includes_reference_and_ineffective_repair_for_both_check_sets(self):
        for case in self.manifest["cases"]:
            _, result = case_suite.calibrate(self.run.path, case, "sha256:" + "a" * 64)
            self.assertEqual(result["outcome"], "case_calibration_passed", result.get("reason"))
            self.assertEqual(len(result["observations"]), 8)
            for observation in result["observations"]:
                self.assertEqual(observation["actual"], observation["expected"])

    def test_modified_private_check_blocks_before_container_execution(self):
        case = self.manifest["cases"][1]
        path = self.run.path / "cases" / case["id"] / "private/verifier.go.txt"
        path.write_text("Modified verifier")
        with patch("case_suite.preflight") as preflight:
            _, result = case_suite.calibrate(self.run.path, case, "sha256:" + "a" * 64)
        self.assertEqual(result["outcome"], "invalid_input")
        preflight.assert_not_called()

    def test_extra_verifier_file_cannot_sneak_into_an_agent_project(self):
        case = self.manifest["cases"][1]
        directory = self.run.path / "cases" / case["id"]
        (directory / "agent" / case_suite.PRIVATE_FILE).write_text("Hidden test leaked")
        with self.assertRaises(Stop):
            case_suite.validate_case(directory, case)

    def test_changed_manifest_and_linked_source_are_refused(self):
        manifest = self.run.path / "suite.json"
        manifest.write_text(manifest.read_text() + "\n")
        with self.assertRaises(Stop):
            case_suite.load_suite(self.run.path)
        case = self.manifest["cases"][0]
        directory = self.run.path / "cases" / case["id"]
        path = directory / "agent" / case_suite.SOURCE
        path.unlink()
        path.symlink_to(directory / "private/original-import.go.txt")
        with self.assertRaises(Stop):
            case_suite.validate_case(directory, case)

    def test_missing_named_subtests_cannot_count_as_a_calibrated_case(self):
        def missing(run, name, args, cwd, build_output=False):
            if name == "go-version":
                return {"exit_code": 0}, b"go version go1.27.1 linux/amd64\n"
            check, _ = self.execute(run, name, args, cwd, build_output)
            check["exit_code"] = 0
            output = b'{"Action":"pass","Test":"TestHarnessCoordinateLimits"}'
            (run.path / check["stdout"]).write_bytes(output)
            return check, output

        self.execute_mock.side_effect = missing
        _, result = case_suite.calibrate(self.run.path, self.manifest["cases"][1], "sha256:" + "a" * 64)
        self.assertEqual(result["outcome"], "calibration_failed")

    def test_interruption_keeps_the_stopped_case_and_remaining_case_visible(self):
        batch = Run(self.root / "batches")
        batch.result["cases"] = [{"id": case["id"], "status": "pending"} for case in self.manifest["cases"]]
        self.execute_mock.side_effect = KeyboardInterrupt
        with self.assertRaises(KeyboardInterrupt):
            case_suite.calibrate(self.run.path, self.manifest["cases"][0], "sha256:" + "a" * 64, batch)
        self.assertEqual(batch.result["cases"][0]["outcome"], "cancelled")
        self.assertEqual(batch.result["cases"][1]["status"], "pending")
        self.assertIn("Not run", case_suite.render_batch(batch))

    def test_cli_can_calibrate_one_selected_case_without_repeating_the_other(self):
        selected = self.manifest["cases"][1]
        with patch("case_suite.calibrate", wraps=case_suite.calibrate) as calibrate, patch("sys.argv", [
                "case_suite.py", "--calibrate", str(self.run.path), "--case", selected["id"],
                "--image", "sha256:" + "a" * 64]):
            self.assertEqual(case_suite.main(), 0)
        self.assertEqual(calibrate.call_count, 1)
        self.assertEqual(calibrate.call_args.args[1]["partition"], "held-out")

    def test_an_unrelated_assertion_failure_does_not_count_as_the_intended_bug(self):
        case = self.manifest["cases"][1]
        output = b'\n'.join(json.dumps({"Action": "fail", "Test": identity}).encode() for identity in
                            [case["public_test"], *(case["public_test"] + "/" + name for name in case["public_cases"])])
        self.assertEqual(case_suite.classify_calibration(output, 1, case, "public"), "unexpected_failure")


if __name__ == "__main__":
    unittest.main()
