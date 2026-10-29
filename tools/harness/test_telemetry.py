import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import telemetry


class TelemetryTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.output = self.root / "telemetry.html"

    def receipt(self, identity, **extra):
        directory = self.root / "pi/runs" / identity
        directory.mkdir(parents=True)
        value = dict(run_id=identity, outcome="repair_failed", model="example", candidate_source="AI", **extra)
        (directory / "result.json").write_text(json.dumps(value))
        (directory / "events.jsonl").write_text("")
        return directory

    def test_empty_report_does_not_invent_trends(self):
        page = telemetry.render(telemetry.load(self.root), self.root, self.output)
        self.assertIn("No runs recorded", page)
        self.assertNotIn("submitted tasks</summary>", page)
        self.assertIn("Not a live feed", page)

    def test_failure_cancellation_incomplete_and_unknowns_remain_in_denominator(self):
        for index, outcome in enumerate(("repair_failed", "cancelled", "incomplete", "blocked", "timeout")):
            directory = self.receipt(str(index))
            value = json.loads((directory / "result.json").read_text())
            value["outcome"] = outcome
            (directory / "result.json").write_text(json.dumps(value))
        value = telemetry.load(self.root)
        self.assertEqual(len(telemetry.groups(value["records"])[0]), 5)
        page = telemetry.render(value, self.root, self.output)
        self.assertIn("Eventual checked fixes: 0/5", page)
        self.assertIn("Model cost: Unknown", page)
        self.assertIn("Human review time: Unknown", page)
        self.assertNotIn("<meter", page)
        for outcome in ("repair_failed", "cancelled", "incomplete", "blocked", "timeout"):
            self.assertIn(f"{outcome}: 1", page)

    def test_mixed_task_model_settings_and_demonstrations_are_separate(self):
        for index, extra in enumerate(({}, {"task_sha256": "other-task"}, {"config": {"reasoning": "high"}},
                                       {"environment": {"os": "different"}}, {"candidate_source": "supplied"})):
            directory = self.receipt(str(index))
            value = json.loads((directory / "result.json").read_text())
            value.update(extra)
            (directory / "result.json").write_text(json.dumps(value))
        self.assertEqual(len(telemetry.groups(telemetry.load(self.root)["records"])), 5)

    def test_event_replay_is_deduplicated_but_conflicts_and_partial_lines_are_visible(self):
        directory = self.receipt("run")
        event = dict(schema_version=1, run_id="run", event_id="event", kind="model_request", at="2026-10-29")
        conflict = dict(event, kind="model_response")
        (directory / "events.jsonl").write_text('\n'.join(map(json.dumps, [event, event, conflict])) + '\n{partial')
        rows, problems, fingerprint = telemetry.events(directory, "run")
        self.assertEqual(rows, [event])
        self.assertEqual(len(problems), 2)
        self.assertIn("Conflicting duplicate", problems[0])
        self.assertIsNotNone(fingerprint)
        page = telemetry.render(telemetry.load(self.root), self.root, self.output)
        self.assertIn("Journal line 4", page)
        self.assertIn("timeline (1 events)", page)

    def test_foreign_event_and_linked_journal_are_not_read_as_run_evidence(self):
        directory = self.receipt("run")
        event = dict(schema_version=1, run_id="other", event_id="event", kind="model_request")
        journal = directory / "events.jsonl"
        journal.write_text(json.dumps(event))
        self.assertEqual(telemetry.events(directory, "run")[0], [])
        journal.unlink()
        journal.symlink_to(directory / "result.json")
        value = telemetry.load(self.root)
        self.assertEqual(value["records"][0]["events"], [])
        self.assertTrue(value["records"][0]["event_problems"])

    def test_child_and_duplicate_run_ids_do_not_inflate_task_totals(self):
        directory = self.receipt("parent")
        self.receipt("child", parent_run_id="parent")
        duplicate = self.root / "native-tools/runs/duplicate"
        duplicate.mkdir(parents=True)
        (duplicate / "result.json").write_bytes((directory / "result.json").read_bytes())
        (duplicate / "events.jsonl").write_text("")
        value = telemetry.load(self.root)
        self.assertEqual(sum(map(len, telemetry.groups(value["records"]))), 1)
        self.assertEqual(sum(r["duplicate"] for r in value["records"]), 1)
        self.assertIn("Identical run replay", telemetry.render(value, self.root, self.output))
        record = json.loads((duplicate / "result.json").read_text())
        record["outcome"] = "cancelled"
        (duplicate / "result.json").write_text(json.dumps(record))
        value = telemetry.load(self.root)
        self.assertEqual(sum(map(len, telemetry.groups(value["records"]))), 0)
        self.assertIn("Conflicting evidence", telemetry.render(value, self.root, self.output))

    def test_unconfirmed_pass_never_becomes_checked_success_and_html_is_inert(self):
        directory = self.receipt("run")
        value = json.loads((directory / "result.json").read_text())
        value.update(outcome="native_verification_passed", patch_sha256="<script>alert(1)</script>")
        (directory / "result.json").write_text(json.dumps(value))
        page = telemetry.render(telemetry.load(self.root), self.root, self.output)
        self.assertIn("Saved pass could not be confirmed", page)
        self.assertIn("Eventual checked fixes: 0/1", page)
        self.assertIn("&lt;script&gt;", page)
        self.assertNotIn("<script>alert(1)</script>", page)

    def test_missing_receipt_keeps_the_run_and_its_events_visible(self):
        directory = self.receipt("unfinished")
        (directory / "result.json").unlink()
        page = telemetry.render(telemetry.load(self.root), self.root, self.output)
        self.assertIn("unfinished", page)
        self.assertIn("evidence_unreadable: 1", page)
        self.assertIn("Receipt unavailable", page)

    def test_partial_attempt_and_event_details_do_not_abort_the_report(self):
        directory = self.receipt("partial")
        value = json.loads((directory / "result.json").read_text())
        value.update(run_id=["invalid"], verification_attempts=[None, {"checks": None}, {"checks": ["partial"]}])
        (directory / "result.json").write_text(json.dumps(value))
        event = dict(schema_version=1, run_id="partial", event_id="event", kind="command_finished", check=None)
        (directory / "events.jsonl").write_text(json.dumps(event))
        page = telemetry.render(telemetry.load(self.root), self.root, self.output)
        self.assertIn("Invalid run ID", page)
        self.assertIn("Unreadable command event", page)
        self.assertIn("Unreadable attempt details", page)

    def test_missing_journal_exits_one_but_still_builds_the_report(self):
        directory = self.receipt("run")
        (directory / "events.jsonl").unlink()
        with patch("sys.argv", ["telemetry.py", "--reports", str(self.root)]):
            self.assertEqual(telemetry.main(), 1)
        self.assertIn("Event journal unavailable", self.output.read_text())

    def test_source_change_during_render_keeps_previous_page(self):
        directory = self.receipt("run")
        self.output.write_text("previous page")
        render = telemetry.render
        def changed(value, reports, output):
            page = render(value, reports, output)
            (directory / "events.jsonl").write_text("changed during render")
            return page
        with patch("telemetry.render", side_effect=changed), self.assertRaisesRegex(ValueError, "Evidence changed"):
            telemetry.build(self.root, self.output, telemetry.HERE / "progress.json")
        self.assertEqual(self.output.read_text(), "previous page")

    def test_future_receipt_version_cannot_be_silently_interpreted(self):
        directory = self.receipt("future", schema_version=2)
        value = telemetry.load(self.root)
        self.assertIn("Unsupported receipt schema", value["records"][0]["problem"])
        self.assertFalse(value["records"][0]["verified_success"])

    def test_malformed_identity_fields_do_not_hide_other_runs_or_abort_rendering(self):
        self.receipt("valid")
        for index, (field, invalid) in enumerate((("model", []), ("model", {}), ("task", []), ("task", {}),
                                                ("workflow", []), ("synthetic", []))):
            directory = self.receipt(f"malformed-{index}")
            path = directory / "result.json"
            result = json.loads(path.read_text())
            result[field] = invalid
            path.write_text(json.dumps(result))
        value = telemetry.load(self.root)
        self.assertEqual(len(value["records"]), 7)
        invalid_rows = [r for r in value["records"] if r.get("problem")]
        self.assertEqual(len(invalid_rows), 6)
        self.assertTrue(all(not r["verified_success"] for r in invalid_rows))
        page = telemetry.render(value, self.root, self.output)
        self.assertIn("Malformed receipt metadata", page)
        self.assertIn("Unknown model", page)
        self.assertIn("valid", page)
        with patch("sys.argv", ["telemetry.py", "--reports", str(self.root)]):
            self.assertEqual(telemetry.main(), 1)
        self.assertTrue(self.output.is_file())

    def test_retry_success_counts_one_task_and_keeps_failed_attempt(self):
        directory = self.receipt("retry")
        value = json.loads((directory / "result.json").read_text())
        value.update(workflow="native-repair-workflow", outcome="native_verification_passed", first_repair_passed=False,
                     verification_attempts=[{"outcome": "repair_failed", "patch_sha256": "first"},
                                            {"outcome": "repository_verification_passed", "patch_sha256": "second"}])
        (directory / "result.json").write_text(json.dumps(value))
        with patch("evaluate.verified", return_value=True):
            page = telemetry.render(telemetry.load(self.root), self.root, self.output)
        self.assertIn("First-repair checked fixes: 0/1", page)
        self.assertIn("Eventual checked fixes: 1/1", page)
        self.assertIn("Attempt result: repair_failed", page)
        self.assertIn("first</code>", page)
        self.assertIn("second</code>", page)

    def test_regeneration_preserves_source_and_reproduces_body_offline(self):
        directory = self.receipt("run", duration_seconds=0)
        original = {p: p.read_bytes() for p in directory.iterdir()}
        args = ["telemetry.py", "--reports", str(self.root), "--output", str(self.output)]
        with patch("sys.argv", args), patch("evaluate.now", return_value="fixed"):
            self.assertEqual(telemetry.main(), 0)
            first = self.output.read_bytes()
            self.assertEqual(telemetry.main(), 0)
        self.assertEqual(first, self.output.read_bytes())
        self.assertEqual(original, {p: p.read_bytes() for p in directory.iterdir()})
        self.assertIn('value="0"', first.decode())


if __name__ == "__main__":
    unittest.main()
