import copy
import json
from pathlib import Path
import tempfile
import shutil
import unittest
from unittest.mock import patch

import measurements
from run import HERE, digest, write_json
import telemetry
from test_repair import RepairFixture


def sample_inventory(root):
    fixture = json.loads((HERE / "fixtures/telemetry.json").read_text())
    outcomes = {}
    for index, case in enumerate(fixture["cases"], 1):
        directory = root / "workflows/runs" / case["id"]
        directory.mkdir(parents=True, exist_ok=True)
        patch_bytes = ("supplied candidate " + case["id"]).encode()
        (directory / "candidate.patch").write_bytes(patch_bytes)
        attempts = [{"run_id": f"{case['id']}-{n}", "patch_sha256": digest(patch_bytes),
                     "outcome": "repository_verification_passed" if n == case["attempts"] and case["verified"] else "repair_failed"}
                    for n in range(1, (case["attempts"] or 0) + 1)]
        result = {"schema_version": 1, "workflow": "native-repair-workflow", "mode": "plain", "run_id": case["id"],
                  "synthetic": True, "candidate_source": "supplied", "evidence_origin": "simulated-check-output",
                  "outcome": case["outcome"], "first_repair_passed": case["first"], "patch_sha256": digest(patch_bytes),
                  "duration_seconds": case["seconds"], "model_cost_usd": case["model_cost"], "execution_cost_usd": case["execution_cost"],
                  "started_at": f"2026-01-01T00:00:0{index}+00:00", "model": "scripted (no AI)",
                  "task": "public-synthetic-task", "task_sha256": "same-fixture", "evaluator": {"suite_version": 1},
                  "evaluator_sha256": "same-suite", "runner_sha256": "same-harness", "human_acceptance": "pending",
                  "usage_summary": {"complete": case["tokens"] is not None, "totals": {"totalTokens": case["tokens"]}},
                  "recovery_outcome": case.get("recovery_outcome"), "recovery": bool(case.get("recovery_outcome"))}
        if case["attempts"] is not None:
            result["verification_attempts"] = attempts
        if case["id"] != "incomplete":
            result["ended_at"] = "2026-01-01T00:01:00+00:00"
        raw = json.dumps(result).encode()
        (directory / "result.json").write_bytes(raw)
        if case["false_claim"] is not None:
            (directory / "claim-review.json").write_text(json.dumps({"runs": [{"run_id": case["id"], "receipt_sha256": digest(raw),
                                                                              "incorrect_success_claim": case["false_claim"]}]}))
        events = [{"schema_version": 1, "run_id": case["id"], "event_id": "start", "kind": "run_started", "at": result["started_at"]}]
        if case.get("recovery_outcome"):
            events.append(dict(events[0], event_id="recovered", kind="operation_reconciled"))
        if result.get("ended_at"):
            events.append(dict(events[0], event_id="finished", kind="run_finished", at=result["ended_at"], outcome=case["outcome"]))
        (directory / "events.jsonl").write_text(''.join(json.dumps(e) + '\n' for e in events))
        outcomes[case["id"]] = case["verified"]
    with patch("evaluate.verified", side_effect=lambda result, *_: outcomes[result["run_id"]]):
        value = telemetry.load(root)
    for row in value["records"]:
        row["demonstration"] = True
    value["demonstration"] = True
    return value, fixture


def write_sample(root):
    value, _ = sample_inventory(root)
    output = root / "synthetic.html"
    output.write_text(telemetry.render(value, root, output))
    return output


class MeasurementTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.value, self.fixture = sample_inventory(self.root)

    def test_known_synthetic_outcomes_and_unknowns_match_expected_totals(self):
        summary = measurements.summarize(self.value["records"])
        for key in ("submitted_tasks", "first_attempt_successes", "eventual_successes", "false_completions",
                    "claim_reviews_missing", "interruptions", "recovery_observations"):
            self.assertEqual(summary[key], self.fixture["expected"][key], key)
        self.assertEqual(summary["metrics"]["tokens"]["known"], 4)
        self.assertEqual(summary["metrics"]["total_cost_usd"]["known"], 3)
        self.assertIsNone(summary["metrics"]["total_cost_usd"]["total"])
        self.assertEqual(summary["metrics"]["duration_seconds"]["median"], 12)
        self.assertEqual(summary["recovery_outcomes"], {"verified after resume": 1})

    def test_supplied_success_expectations_are_not_trusted_by_real_verification(self):
        value = telemetry.load(self.root)
        self.assertEqual(sum(r["verified_success"] for r in value["records"]), 0)
        self.assertEqual(sum(r["unconfirmed_completion"] for r in value["records"]), 3)

    def test_loading_repeatedly_does_not_accumulate_or_overwrite_runs(self):
        paths = [p for p in self.root.rglob('*') if p.is_file()]
        original = {p: p.read_bytes() for p in paths}
        first = telemetry.load(self.root)
        second = telemetry.load(self.root)
        self.assertEqual([r["measurement"] for r in first["records"]], [r["measurement"] for r in second["records"]])
        self.assertEqual(original, {p: p.read_bytes() for p in paths})

    def test_claim_review_is_bound_to_exact_receipt_not_a_model_message(self):
        path = self.root / "workflows/runs/failure/result.json"
        result = json.loads(path.read_text())
        result["model_claim"] = "done"
        path.write_text(json.dumps(result))
        row = next(r for r in telemetry.load(self.root)["records"] if r["run_id"] == "failure")
        self.assertIsNone(row["measurement"]["false_completion"])
        self.assertFalse(row["verified_success"])

    def test_projection_fields_match_the_versioned_schema(self):
        schema = json.loads((HERE / "measurement-schema.json").read_text())
        for row in self.value["records"]:
            value = row["measurement"]
            self.assertEqual(value["schema_version"], schema["properties"]["schema_version"]["const"])
            self.assertTrue(set(schema["required"]).issubset(value))
            self.assertTrue(set(schema["properties"]["identity"]["required"]).issubset(value["identity"]))
            self.assertTrue(set(schema["properties"]["metrics"]["required"]).issubset(value["metrics"]))
            for number in value["metrics"].values():
                self.assertTrue(number is None or measurements.evaluate.number(number) is not None)

    def test_small_samples_show_raw_points_without_invented_precision(self):
        self.assertIsNone(measurements.interval(0, 0))
        band = measurements.interval(3, 6)
        self.assertAlmostEqual(band[0], 1 - band[1])
        self.assertLess(band[0], .5)
        page = telemetry.render(self.value, self.root, self.root / "sample.html")
        self.assertIn("Known / submitted", page)
        self.assertIn("No paired comparison evidence", page)
        self.assertIn("Demonstration data", page)
        self.assertIn("operation_reconciled", json.dumps(self.value["records"]))

    def test_missing_cost_component_prevents_a_partial_total(self):
        row = next(r for r in self.value["records"] if r["run_id"] == "success")
        row["execution_cost_usd"] = None
        value = measurements.normalize(row, {}, self.root)
        self.assertIsNone(value["metrics"]["total_cost_usd"])

    def test_null_or_malformed_attempt_history_stays_unknown_but_empty_list_is_zero(self):
        path = self.root / "workflows/runs/cancelled/result.json"
        original = json.loads(path.read_text())
        for field in ("attempts", "verification_attempts"):
            for history in (None, "partial", 1, {}, []):
                with self.subTest(field=field, history=history):
                    result = dict(original)
                    result.pop("attempts", None)
                    result.pop("verification_attempts", None)
                    result[field] = history
                    path.write_text(json.dumps(result))
                    row = next(r for r in telemetry.load(self.root)["records"] if r["run_id"] == "cancelled")
                    expected = 0 if history == [] else None
                    self.assertEqual(row["measurement"]["metrics"]["repair_attempts"], expected)
                    self.assertEqual(row["measurement"]["metrics"]["retries"], expected)

    def test_task_label_uses_only_the_hash_bound_saved_task(self):
        row = copy.deepcopy(self.value["records"][0])
        raw = json.dumps({"id": "saved-task-name"}).encode()
        path = self.root / Path(row["receipt"]).parent / "verification-task.json"
        path.write_bytes(raw)
        row["receipt_snapshot"].update(task=None, task_sha256=digest(raw))
        self.assertEqual(measurements.normalize(row, {}, self.root)["identity"]["task"], "saved-task-name")
        path.write_text(json.dumps({"id": "changed-task-name"}))
        self.assertIsNone(measurements.normalize(row, {}, self.root)["identity"]["task"])

    def test_unknown_token_usage_and_nonfinite_numbers_are_not_zero(self):
        row = copy.deepcopy(self.value["records"][0])
        row["token_summary"] = {"complete": False, "totals": {"totalTokens": 42}}
        value = measurements.normalize(row, {}, self.root)
        self.assertIsNone(value["metrics"]["tokens"])
        value = measurements.distribution([0, None, float('nan'), 5])
        self.assertEqual(value["known"], 2)
        self.assertEqual(value["missing"], 2)
        self.assertIsNone(value["total"])

    def test_paired_report_is_read_only_and_refuses_incomplete_cells(self):
        from test_workflow_compare import WorkflowComparisonTests
        import workflow_compare
        fixture = WorkflowComparisonTests()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        path = self.root / "workflow-pairs/experiments/experiment"
        shutil.copytree(fixture.path, path)
        plan = workflow_compare.load_plan(path, execution=False)
        records = self.value["records"][:4]
        observations = [{"status": "finished", "run_id": r["run_id"], "receipt_sha256": r["receipt_sha256"]} for r in records]
        original = {p: p.read_bytes() for p in path.rglob('*') if p.is_file()}
        with patch("workflow_compare.collect", side_effect=observations):
            values = measurements.comparisons(self.root, records)
        self.assertTrue(values[0]["complete"])
        self.assertEqual(len(values[0]["rows"]), 4)
        self.assertEqual(original, {p: p.read_bytes() for p in path.rglob('*') if p.is_file()})
        observations[1] = {"status": "incomplete"}
        with patch("workflow_compare.collect", side_effect=observations):
            values = measurements.comparisons(self.root, records)
        self.assertFalse(values[0]["complete"])
        self.assertIn("unfinished", values[0]["problem"])
        self.assertEqual(len(plan["cells"]), 4)


class VerifiedMeasurementTests(RepairFixture, unittest.TestCase):
    def test_retry_checks_keep_their_own_timing_and_exact_patch_evidence(self):
        self.dispatch("retry")
        self.tools.finish()
        write_json(self.run.path / "result.json", self.run.result)
        value = telemetry.load(self.root)
        row = next(r for r in value["records"] if not r["is_child"])
        measured = row["measurement"]
        self.assertTrue(measured["eventual_success"])
        self.assertFalse(measured["first_attempt_success"])
        self.assertEqual(measured["metrics"]["repair_attempts"], 2)
        self.assertEqual(measured["metrics"]["retries"], 1)
        checks = [c for c in measured["checks"] if c["attempt"] is not None]
        self.assertEqual({c["attempt"] for c in checks}, {1, 2})
        self.assertTrue(all(c["started_at"] and c["ended_at"] and c["duration_seconds"] is not None for c in checks))
        self.assertEqual(len({c["patch_sha256"] for c in checks}), 2)
        self.assertTrue(all((self.root / c["receipt"]).is_file() for c in checks))
        child = self.run.result["attempts"][-1]["verification_run_id"]
        (self.root / "verification/runs" / child / "candidate.stdout").write_text("Changed after verification")
        row = next(r for r in telemetry.load(self.root)["records"] if not r["is_child"])
        self.assertFalse(row["measurement"]["eventual_success"])
        self.assertTrue(row["measurement"]["unconfirmed_completion"])


if __name__ == "__main__":
    unittest.main()
