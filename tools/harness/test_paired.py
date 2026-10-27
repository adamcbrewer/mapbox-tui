import copy
import json
from pathlib import Path
import tempfile
import shutil
import unittest
from unittest.mock import patch

import paired
from run import Stop, digest, now, write_json
from verify import fingerprint


class PairedFixture:
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.common = {"context": {"prompt": "Original task and approved instructions", "systemPrompt": "standard Pi", "tools": ["read", "edit", "write", "bash"],
                                   "activeTools": ["read", "edit", "write", "bash"], "task": {"id": "development"}, "nodeVersion": "v26"},
                       "config": {"provider": "openai", "auth": "subscription", "model": "chosen", "maxModelRequests": 10}, "image": "fixed-image", "budgets": {"deadline_seconds": 600},
                       "evaluator": {"fixed": "rules"}}
        self.cells = []
        for position, (pair, arm) in enumerate(paired.ORDER, 1):
            cell = {"position": position, "pair": pair, "arm": arm, "experiment_id": self.root.name}
            child = copy.deepcopy(self.common)
            child["comparison"] = cell.copy()
            if arm == "guidance":
                child["context"]["prompt"] += paired.GUIDANCE
            directory = self.root / "cells" / str(position)
            directory.mkdir(parents=True)
            (directory / "plan.json").write_text(json.dumps({"definition": child, "sha256": fingerprint(child)}))
            self.cells.append({**cell, "plan_sha256": fingerprint(child)})
        self.plan = {"kind": "container-guidance-pairs-v1", "common": self.common, "sources": {"frozen": "source"},
                     "guidance": paired.GUIDANCE, "cells": self.cells}
        self.save()
        self.enterContext(patch("paired.sources", return_value={"frozen": "source"}))
        self.enterContext(patch("paired.baseline.load_plan", side_effect=lambda path: json.loads((path / "plan.json").read_text())["definition"]))

    def save(self):
        (self.root / "plan.json").write_text(json.dumps({"definition": self.plan, "sha256": fingerprint(self.plan)}))


class PairedTests(PairedFixture, unittest.TestCase):
    def test_prepare_keeps_setup_and_comparison_at_the_same_path(self):
        directory = self.root / "new-experiment"
        directory.mkdir()
        write_json(directory / "plan.json", {"definition": self.common})
        with patch("paired.baseline.prepare", return_value=directory) as prepare, patch("paired.probe", return_value={"confirmed": True}):
            path = paired.prepare(Path("suite"), "image", Path("config"))
        self.assertEqual(path, directory)
        self.assertEqual(prepare.call_args.kwargs["output_root"], paired.REPORTS / "native-pairs/experiments")
        self.assertEqual(len(json.loads((path / "plan.json").read_text())["definition"]["cells"]), 4)

    def rows(self, failures=(4, 0, 0, 3)):
        return [{**cell, "status": "finished", "verified": True, "agent_outcome": "finished",
                 "failed_tools": failures[index], "tools": 10, "requests": 7, "duration_seconds": 260,
                 "usage": {"complete": True, "totals": {"totalTokens": 28000}}} for index, cell in enumerate(self.cells)]

    def test_reverse_order_changes_only_the_added_prompt_text(self):
        plan = paired.load_plan(self.root)
        self.assertEqual([(c["pair"], c["arm"]) for c in plan["cells"]], list(paired.ORDER))
        for cell in plan["cells"]:
            child = json.loads((self.root / "cells" / str(cell["position"]) / "plan.json").read_text())["definition"]
            original = child.pop("comparison")
            self.assertEqual(original["arm"], cell["arm"])
            if cell["arm"] == "guidance":
                self.assertTrue(child["context"]["prompt"].endswith(paired.GUIDANCE))
                child["context"]["prompt"] = child["context"]["prompt"][:-len(paired.GUIDANCE)]
            self.assertEqual(child, self.common)
        self.assertNotIn("BOM", paired.GUIDANCE)

    def test_changed_model_tools_checks_or_limits_are_refused(self):
        path = self.root / "cells/2/plan.json"
        original = path.read_text()
        for key in ("config", "image", "budgets", "evaluator"):
            child = json.loads(original)
            child["definition"][key] = "changed"
            path.write_text(json.dumps(child))
            with self.subTest(key=key), self.assertRaises(Stop):
                paired.load_plan(self.root)
        child = json.loads(original)
        child["definition"]["context"]["tools"] = ["other"]
        path.write_text(json.dumps(child))
        with self.assertRaises(Stop):
            paired.load_plan(self.root)

    def test_changed_order_and_source_are_refused(self):
        self.plan["cells"] = list(reversed(self.cells))
        self.save()
        with self.assertRaises(Stop):
            paired.load_plan(self.root)
        self.plan["cells"] = self.cells
        self.plan["sources"] = {"other": "source"}
        self.save()
        with self.assertRaises(Stop):
            paired.load_plan(self.root)

    def test_historical_reports_allow_new_source_but_execution_stays_refused(self):
        with patch("paired.sources", return_value={"new": "source"}), patch("paired.collect", side_effect=self.rows()), \
                patch("paired.baseline.execute") as run:
            report = paired.summarize(self.root)
            self.assertTrue(report["complete"])
            self.assertIn("chosen", (self.root / "comparison.md").read_text())
            self.assertNotIn("Luna", (self.root / "comparison.md").read_text())
            with self.assertRaises(Stop):
                paired.execute(self.root)
        run.assert_not_called()

    def test_consistent_reduction_requires_all_four_checked_repairs(self):
        with patch("paired.collect", side_effect=self.rows()):
            result = paired.summarize(self.root)
        self.assertEqual(result["failed_call_reductions"], [4, 3])
        self.assertTrue(result["primary_improved_in_both_pairs"])
        self.assertIn("awaiting", result["adoption"])
        rows = self.rows()
        rows[1]["verified"] = False
        with patch("paired.collect", side_effect=rows):
            result = paired.summarize(self.root)
        self.assertFalse(result["primary_improved_in_both_pairs"])

    def test_mixed_pairs_do_not_become_an_improvement_from_the_aggregate(self):
        with patch("paired.collect", side_effect=self.rows((7, 0, 3, 0))):
            result = paired.summarize(self.root)
        self.assertEqual(result["failed_call_reductions"], [7, -3])
        self.assertFalse(result["primary_improved_in_both_pairs"])

    def test_missing_runs_and_usage_remain_unknown(self):
        rows = self.rows()
        rows[0]["usage"] = {"complete": False}
        rows[3] = {**self.cells[3], "status": "not_started"}
        with patch("paired.collect", side_effect=rows):
            result = paired.summarize(self.root)
        self.assertFalse(result["complete"])
        self.assertEqual(result["failed_call_reductions"], [])
        self.assertIn("unknown", (self.root / "comparison.md").read_text())

    def test_runs_follow_saved_order_once(self):
        calls = []
        def execute(path):
            calls.append(int(path.name))
            directory = self.root / ("run-" + path.name)
            directory.mkdir()
            (directory / "result.json").write_text("{}")
            return directory, {"outcome": "native_verification_passed"}
        with patch("paired.baseline.execute", side_effect=execute), patch("paired.summarize", return_value={"complete": True}):
            paired.execute(self.root)
            with self.assertRaises(Stop):
                paired.execute(self.root)
        self.assertEqual(calls, [1, 2, 3, 4])

    def test_cancelled_or_blocked_cell_stops_without_replacement(self):
        for outcome in ("cancelled", "blocked"):
            (self.root / "attempt.json").unlink(missing_ok=True)
            directory = self.root / outcome
            directory.mkdir()
            (directory / "result.json").write_text("{}")
            with self.subTest(outcome=outcome), patch("paired.baseline.execute", return_value=(directory, {"outcome": outcome})) as run, \
                    patch("paired.summarize", return_value={"complete": False}), self.assertRaises(Stop):
                paired.execute(self.root)
            self.assertEqual(run.call_count, 1)

    def test_prestarted_cell_is_not_adopted_as_a_new_pair(self):
        (self.root / "cells/4/attempt.json").write_text('{"run_id":"prior"}')
        with patch("paired.baseline.execute") as run, self.assertRaises(Stop):
            paired.execute(self.root)
        run.assert_not_called()


class PairedReceiptTests(PairedFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        from test_native_check import NativeCheckTests
        import native_check
        fixture = NativeCheckTests()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        fixture.candidate()
        native_check.check_candidate(fixture.run)
        result = fixture.run.result
        identity = fixture.run.path.name
        self.run_path = self.root / "native-baseline/runs" / identity
        shutil.copytree(fixture.run.path, self.run_path)
        shutil.copytree(fixture.root / "verification", self.root / "verification")
        self.cell = self.cells[0]
        self.result = copy.deepcopy(result)
        self.common.update(image=result["image"], evaluator=result["evaluator"])
        for cell in self.cells:
            child = copy.deepcopy(self.common)
            child["comparison"] = {key: cell[key] for key in ("position", "pair", "arm", "experiment_id")}
            if cell["arm"] == "guidance":
                child["context"]["prompt"] += paired.GUIDANCE
            cell["plan_sha256"] = fingerprint(child)
            write_json(self.root / "cells" / str(cell["position"]) / "plan.json", {"definition": child, "sha256": fingerprint(child)})
        self.save()
        self.result.update(workflow="plain-pi-live-baseline", candidate_source="AI", model="chosen", provider="openai", auth="subscription",
                           plan_sha256=self.cell["plan_sha256"], comparison={key: self.cell[key] for key in ("position", "pair", "arm", "experiment_id")},
                           budgets=self.common["budgets"], ended_at=now(), duration_seconds=200, agent_outcome="finished",
                           tool_results=[{"name": "bash", "error": "Expected tool error"}], tool_calls=1,
                           model_requests=1, dispatches=[1], provider_usage=[{"request": 1, "usage": {
                               "inputTokens": 10, "cachedInputTokens": 0, "outputTokens": 2, "totalTokens": 12, "reasoningTokens": 0}}])
        (self.root / "cells/1/attempt.json").write_text(json.dumps({"run_id": identity}))
        write_json(self.run_path / "context.json", {**self.common["context"], "permissions": {"network": True, "childProcess": False}})
        write_json(self.run_path / "reservations.json", [{"request": 1, "provider": "openai", "model": "chosen"}])
        write_json(self.run_path / "result.json", self.result)
        self.pin()
        self.enterContext(patch("paired.REPORTS", self.root))

    def pin(self):
        write_json(self.root / "progress.json", {"cells": [{"position": 1, "status": "finished", "run_id": self.run_path.name,
                   "receipt_sha256": digest((self.run_path / "result.json").read_bytes())}]})

    def test_checked_fix_is_bound_to_the_plan_and_usage_is_recomputed(self):
        row = paired.collect(self.root, self.cell)
        self.assertTrue(row["verified"])
        self.assertEqual(row["failed_tools"], 1)
        self.assertEqual(row["usage"]["totals"]["totalTokens"], 12)

    def test_changed_model_context_source_or_check_log_cannot_keep_a_pass(self):
        child = self.root / "verification/runs" / self.result["verification"]["run_id"]
        files = {path: path.read_bytes() for path in (self.run_path / "context.json", self.run_path / "candidate-source.bin", child / "candidate.stdout")}
        for change in ("model", "context", "source", "log", "dispatch"):
            result = copy.deepcopy(self.result)
            for path, raw in files.items():
                path.write_bytes(raw)
            if change == "model":
                result["model"] = "other"
            elif change == "context":
                write_json(self.run_path / "context.json", {**self.common["context"], "prompt": "changed", "permissions": {"network": True, "childProcess": False}})
            elif change == "source":
                (self.run_path / "candidate-source.bin").write_bytes(b"changed")
            elif change == "log":
                (child / "candidate.stdout").write_bytes(b"changed")
            else:
                result["dispatches"] = [1, 1]
            write_json(self.run_path / "result.json", result)
            self.pin()
            with self.subTest(change=change), self.assertRaises(Stop):
                paired.collect(self.root, self.cell)


if __name__ == "__main__":
    unittest.main()
