import base64
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import timeout_recovery as recovery
from run import Stop, digest
from verify import fingerprint


class TimeoutRecoveryTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.context = {key: key for key in recovery.baseline.CONTEXT}
        self.context["prompt"] = recovery.prompt("clear")
        self.common = {"context": self.context, "config": {"provider": "openai", "model": "gpt-6.1-terra", "maxModelRequests": 10},
                       "budgets": {"tool_calls": 12, "model_requests": 10, "deadline_seconds": 600},
                       "case": {"agent_files_sha256": {"go/import.go": "source"}}}
        self.path = self.root / "experiment"
        self.path.mkdir()
        with patch.object(recovery.baseline, "prepare", return_value=self.path), \
                patch.object(recovery.baseline, "load_plan", return_value=copy.deepcopy(self.common)), \
                patch.object(recovery.baseline, "preview", return_value=copy.deepcopy(self.context)):
            recovery.prepare(self.root, "image", self.root / "config")
        self.plan = recovery.load_plan(self.path, execution=False)

    def test_only_feedback_differs_and_order_is_reversed(self):
        self.assertIn("gpt-6.1-terra", (self.path / "plan.md").read_text())
        self.assertNotIn("Luna", (self.path / "plan.md").read_text())
        self.assertEqual([(c["pair"], c["version"]) for c in self.plan["cells"]], list(recovery.ORDER))
        self.assertEqual(self.plan["maximum_model_requests"], 40)
        first = recovery.read_child(self.path, self.plan["cells"][0])
        second = recovery.read_child(self.path, self.plan["cells"][1])
        for child in (first, second):
            child.pop("comparison")
            child.pop("feedback")
        first["context"]["prompt"] = first["context"]["prompt"].replace(recovery.MESSAGES["generic"], recovery.MESSAGES["clear"])
        self.assertEqual(first, second)

    def test_changed_code_is_refused_before_task_loading(self):
        with patch.object(recovery, "sources", return_value={}), patch.object(recovery.baseline, "load_suite") as load:
            with self.assertRaises(Stop):
                recovery.load_plan(self.path)
        load.assert_not_called()
        recovery.load_plan(self.path, execution=False)

    def test_changed_child_and_extra_cell_changes_are_refused(self):
        cell = self.plan["cells"][0]
        child_path = self.path / "cells/1/plan.json"
        saved = json.loads(child_path.read_text())
        saved["definition"]["config"]["model"] = "different"
        child_path.write_text(json.dumps(saved))
        with self.assertRaises(Stop):
            recovery.load_plan(self.path, execution=False)
        saved["sha256"] = fingerprint(saved["definition"])
        child_path.write_text(json.dumps(saved))
        cell["plan_sha256"] = saved["sha256"]
        (self.path / "plan.json").write_text(json.dumps({"definition": self.plan, "sha256": fingerprint(self.plan)}))
        with self.assertRaises(Stop):
            recovery.load_plan(self.path, execution=False)

    def command_evidence(self, identity="native-op-2"):
        directory = self.root / "run"
        directory.mkdir()
        raw = json.dumps({"ok": True, "value": {"exitCode": 0, "output": base64.b64encode(b"/workspace\n").decode()}}).encode()
        (directory / (identity + ".stdout")).write_bytes(raw)
        (directory / "project-snapshot.json").write_text(json.dumps({"files": self.common["case"]["agent_files_sha256"], "invalid": []}))
        plan = {**self.common, "sources": {"native_worker.py": digest(b"worker")}}
        operation = {"op": "bash", "command": "pwd", "timeout": 120}
        result = {"snapshot_scope_valid": True, "changed_files": [], "containers": [{"name": "owned", "removed": True}],
                  "dispatches": [1], "model_requests": 1, "tool_results": [{"name": "bash", "error": None}],
                  "command_checks": [{"check_id": identity, "stdout_sha256": digest(raw), "live_call": 1, "model_requests": 1}],
                  "checks": [{"id": identity, "stdout": identity + ".stdout", "exit_code": 0,
                              "command": ["podman", "--remote=false", "exec", "owned", "/usr/bin/python3", "-I", "-c", "worker", json.dumps(operation)]}]}
        return result, directory, plan

    def test_namespaced_command_evidence_still_requires_the_recorded_log_bytes(self):
        result, directory, plan = self.command_evidence("native-1-op-2")
        self.assertTrue(recovery.recovery_matches(result, directory, plan))
        (directory / "native-1-op-2.stdout").write_text('{}')
        self.assertFalse(recovery.recovery_matches(result, directory, plan))

    def test_claim_without_actual_command_or_unchanged_project_cannot_pass(self):
        result, directory, plan = self.command_evidence()
        self.assertTrue(recovery.recovery_matches(result, directory, plan))
        for field, value in (("command_checks", []), ("dispatches", []), ("changed_files", ["go/import.go"]),
                             ("snapshot_scope_valid", False), ("snapshot_problem", "missing"),
                             ("containers", [{"name": "owned", "removed": False}]),
                             ("tool_results", [{"name": "edit", "error": None}])):
            with self.subTest(field=field):
                self.assertFalse(recovery.recovery_matches({**result, field: value}, directory, plan))
        (directory / "project-snapshot.json").write_text(json.dumps({"files": {}, "invalid": []}))
        self.assertFalse(recovery.recovery_matches(result, directory, plan))

    def test_wrong_command_worker_container_timeout_or_output_cannot_pass(self):
        result, directory, plan = self.command_evidence()
        original = result["checks"][0]["command"]
        for index, value in ((3, "another-container"), (7, "another-worker"),
                             (8, json.dumps({"op": "bash", "command": "printf /workspace", "timeout": 120})),
                             (8, json.dumps({"op": "bash", "command": "pwd", "timeout": 120000})),
                             (8, json.dumps({"op": "bash", "command": "pwd", "timeout": True}))):
            changed = copy.deepcopy(result)
            changed["checks"][0]["command"][index] = value
            with self.subTest(index=index, value=value):
                self.assertFalse(recovery.recovery_matches(changed, directory, plan))
        self.assertEqual(result["checks"][0]["command"], original)
        (directory / "native-op-2.stdout").write_text('{}')
        self.assertFalse(recovery.recovery_matches(result, directory, plan))

    def test_spent_plan_stops_before_any_live_work(self):
        recovery.baseline.claim(self.path, "spent")
        with patch.object(recovery, "load_plan", return_value=self.plan), patch.object(recovery, "run_cell") as run:
            with self.assertRaises(Stop):
                recovery.execute(self.path)
        run.assert_not_called()

    def test_summary_excludes_supplied_failures_and_does_not_call_a_tie_an_improvement(self):
        reports = self.root / "reports"
        progress = {"cells": []}
        for cell in self.plan["cells"]:
            identity = str(cell["position"]) * 32
            directory = reports / "timeout-recovery/runs" / identity
            directory.mkdir(parents=True)
            child = recovery.read_child(self.path, cell)
            result = {"run_id": identity, "comparison": cell, "plan_sha256": cell["plan_sha256"],
                      "model": child["config"]["model"], "budgets": child["budgets"], "recovered": True,
                      "tool_results": [{"name": "bash", "error": recovery.MESSAGES[cell["version"]]}, {"name": "bash", "error": None}],
                      "agent_outcome": "finished", "model_requests": 2, "tool_calls": 2, "duration_seconds": 3,
                      "containers": [{"removed": True}]}
            raw = json.dumps(result).encode()
            (directory / "result.json").write_bytes(raw)
            progress["cells"].append({"position": cell["position"], "status": "finished", "run_id": identity,
                                      "receipt_sha256": digest(raw)})
        (self.path / "progress.json").write_text(json.dumps(progress))
        with patch.object(recovery, "REPORTS", reports), patch.object(recovery, "validate_receipt", return_value={"complete": False}), \
                patch.object(recovery, "recovery_matches", return_value=True):
            result = recovery.summarize(self.path)
            self.assertTrue(result["complete"])
            self.assertTrue(result["guardrail_met"])
            self.assertFalse(result["primary_improved"])
            self.assertEqual(result["reductions"], [0, 0])
            self.assertIn("gpt-6.1-terra", (self.path / "comparison.md").read_text())
            self.assertNotIn("Luna", (self.path / "comparison.md").read_text())
            raw_path = reports / "timeout-recovery/runs" / ("1" * 32) / "result.json"
            raw_path.write_text('{}')
            with self.assertRaises(Stop):
                recovery.summarize(self.path)

    def test_observed_context_or_reservations_cannot_change(self):
        directory = self.root / "run"
        directory.mkdir()
        result = {"run_id": "run", "provider": "openai", "model": self.common["config"]["model"], "budgets": self.common["budgets"],
                  "model_requests": 1, "dispatches": [1], "provider_usage": [], "tool_calls": 1,
                  "tool_results": [{"name": "bash", "error": recovery.MESSAGES["clear"]}]}
        context = {**self.context, "permissions": {"network": True, "childProcess": False}}
        (directory / "context.json").write_text(json.dumps(context))
        reservations = [{"request": 1, "provider": "openai", "model": self.common["config"]["model"]}]
        (directory / "reservations.json").write_text(json.dumps(reservations))
        self.assertFalse(recovery.validate_receipt(result, directory, self.common, recovery.MESSAGES["clear"])["complete"])
        context["prompt"] = "another task"
        (directory / "context.json").write_text(json.dumps(context))
        with self.assertRaises(Stop):
            recovery.validate_receipt(result, directory, self.common, recovery.MESSAGES["clear"])


if __name__ == "__main__":
    unittest.main()
