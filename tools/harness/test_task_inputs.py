import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from case_suite import PUBLIC_FILE, build
from native_check import prepare_verification
from native_sandbox import NativeRun
from run import HERE, Run, Stop, digest
from task_inputs import definition, materials, selected_task
from verify import check_evaluator


class TaskInputTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.suite = Run(self.root / "suites")
        self.case = build(self.suite, "segment-distance-v1")["cases"][0]

    def test_distance_definition_binds_the_seed_and_protected_examples(self):
        task = selected_task(self.suite.path)
        inputs = materials(task)
        self.assertEqual(json.loads(inputs["task"]), task)
        self.assertEqual(digest(inputs["seed"]), self.case["seed_sha256"])
        self.assertEqual(digest(inputs["regression"]), self.case["private_files_sha256"]["verifier.go.txt"])
        self.assertEqual(digest(inputs["public_files"][PUBLIC_FILE]), task["source_sha256"][PUBLIC_FILE])
        self.assertEqual(task["allowed_files"], ["go/import.go"])
        self.assertEqual(task["allowed_new_files"], [])

    def test_default_inputs_remain_the_existing_xml_task(self):
        task = definition()
        inputs = materials(task)
        self.assertEqual(inputs["task"], (HERE / "task.json").read_bytes())
        self.assertEqual(inputs["seed"], (HERE / "fixtures/seed.patch").read_bytes())
        self.assertEqual(inputs["public_files"], {})
        with self.assertRaises(Stop):
            definition("unknown")

    def test_fresh_bounds_task_binds_its_own_seed_and_private_examples(self):
        suite = Run(self.root / "bounds")
        case = build(suite, "geometry-bounds-v1")["cases"][0]
        task = selected_task(suite.path)
        inputs = materials(task)
        self.assertEqual(task["id"], "geometry-bounds-v1")
        self.assertEqual(digest(inputs["seed"]), case["seed_sha256"])
        self.assertEqual(digest(inputs["regression"]), case["private_files_sha256"]["verifier.go.txt"])
        self.assertEqual(task["allowed_files"], ["go/import.go"])
        self.assertEqual(task["allowed_new_files"], [])
        run = NativeRun(self.root / "checking", "sha256:" + "a" * 64)
        prepare_verification(run, case)
        self.assertEqual(run.task["id"], task["id"])

    def test_held_out_task_requires_explicit_selection_and_keeps_its_own_inputs(self):
        suite = Run(self.root / "original-suite")
        manifest = build(suite)
        self.assertEqual(selected_task(suite.path)["id"], "xml-leading-bom-v1")
        task = selected_task(suite.path, "coordinate-latitude-limits-v2")
        case = next(c for c in manifest["cases"] if c["partition"] == "held-out")
        inputs = materials(task)
        self.assertEqual(task["id"], case["id"])
        self.assertEqual(digest(inputs["seed"]), case["seed_sha256"])
        self.assertEqual(digest(inputs["regression"]), case["private_files_sha256"]["verifier.go.txt"])
        self.assertEqual(task["regression"]["test"], case["private_test"])
        self.assertNotEqual(inputs["seed"], materials(definition())["seed"])

    def test_distance_commands_and_permitted_files_cannot_be_relaxed(self):
        task = definition("segment-distance-v1")
        for field, value in (("command", ["true"]), ("allowed_new_files", ["go/shortcut.go"]),
                             ("regression", {"test": "AnotherTest", "cases": []})):
            with self.subTest(field=field), self.assertRaises(Stop):
                materials({**task, field: value})

    def test_changed_seed_or_private_identity_is_refused_before_execution(self):
        for field in ("seed_sha256", "private_files_sha256"):
            case = copy.deepcopy(self.case)
            if field == "seed_sha256":
                case[field] = "changed"
            else:
                case[field]["verifier.go.txt"] = "changed"
            run = NativeRun(self.root / field, "sha256:" + "a" * 64)
            with self.subTest(field=field), self.assertRaises(Stop):
                prepare_verification(run, case)
            self.assertEqual(run.result["containers"], [])

    def test_checking_input_drift_is_refused_after_rules_are_frozen(self):
        run = NativeRun(self.root / "runs", "sha256:" + "a" * 64)
        prepare_verification(run, self.case)
        changed = materials(run.task)
        changed["regression"] += b"\n"
        with patch("verify.materials", return_value=changed), self.assertRaises(Stop):
            check_evaluator(run)
        private = self.suite.path / "cases" / self.case["id"] / "private/verifier.go.txt"
        private.write_bytes(b"changed")
        with self.assertRaises(Stop):
            selected_task(self.suite.path)


if __name__ == "__main__":
    unittest.main()
