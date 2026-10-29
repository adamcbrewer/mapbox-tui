import json
import errno
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import Mock, patch

import report
from run import HERE
from recover import finalize
from recovery import Journal, RecoveryTools
from test_repair import RepairFixture


class SharedReferenceTests(unittest.TestCase):
    def test_developer_reference_matches_the_shared_report_explanations(self):
        self.assertEqual((HERE / "REFERENCE.md").read_text(), report.reference_markdown())

    def test_telemetry_entry_point_keeps_stage_operations_separate(self):
        with patch("sys.argv", ["report.py", "--telemetry", "--refresh-saved"]), self.assertRaises(SystemExit) as error:
            report.main()
        self.assertEqual(error.exception.code, 2)


class ImportReportTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / "source"
        self.success = "workflows/runs/" + "a" * 32
        self.failure = "native-tools/runs/" + "b" * 32
        self.progress = dict(json.loads((HERE / "progress.json").read_text()), stage="Use",
                             import_learning_review={"run": "reports/harness/" + self.success,
                                                     "failure_example": {"run": "reports/harness/" + self.failure}})
        for folder, filename, text in ((self.success, "workflow.md", "Saved fix passed its checks"),
                                       (self.failure, "normal-tools.md", "Separate checker caught the ineffective edit")):
            path = self.source / folder
            path.mkdir(parents=True)
            (path / "result.json").write_text("{}")
            (path / filename).write_text(text)

    def test_use_stage_names_current_import_reports_without_old_xml_instructions(self):
        page = report.render(self.source, self.progress)
        self.assertIn('href="' + self.success + '/workflow.md"', page)
        self.assertIn('href="' + self.failure + '/normal-tools.md"', page)
        self.assertIn("segment-gap task", page)
        self.assertIn("This page has not revalidated", page)
        self.assertNotIn("remove handling for a UTF-8 marker", page)
        self.assertNotIn("No runs recorded", page)
        self.assertNotIn('href="#sandbox"', page)

    def test_extend_stage_uses_contract_reports_and_preserves_them(self):
        self.progress.pop("viewer_learning_review", None)
        self.progress.update(stage="Extend", contract_learning_review=self.progress["import_learning_review"])
        page = report.render(self.source, self.progress)
        self.assertIn("Standalone MCP requires exactly one explicit selector", page)
        self.assertIn('href="' + self.success + '/workflow.md"', page)
        self.assertNotIn("segment-gap task", page)
        self.assertNotIn("remove handling for a UTF-8 marker", page)
        reports = self.root / "extend-reports"
        report.save_stage(reports, self.progress, self.source, source_ref="a" * 40)
        shutil.rmtree(self.source)
        saved = report.render_saved(reports, "extend").read_text()
        self.assertIn('snapshots/extend/' + self.failure + '/normal-tools.md', saved)

    def test_viewer_overview_links_its_own_reports_and_explains_limits(self):
        self.progress.update(stage="Extend", viewer_learning_review=self.progress["import_learning_review"])
        page = report.render(self.source, self.progress)
        self.assertIn("Viewer interaction repairs", page)
        self.assertIn("twenty-minute deadline", page)
        self.assertIn("Structured assertions", page)
        self.assertIn('href="' + self.success + '/workflow.md"', page)
        self.assertIn('href="' + self.failure + '/normal-tools.md"', page)
        self.assertNotIn("CLI inspection defaults to point 0", page)

    def test_saved_use_reports_survive_source_removal_and_bundle_move(self):
        reports = self.root / "reports"
        report.save_stage(reports, self.progress, self.source, source_ref="a" * 40)
        shutil.rmtree(self.source)
        moved = self.root / "moved"
        reports.rename(moved)
        page = report.render_saved(moved, "use").read_text()
        for folder, filename in ((self.success, "workflow.md"), (self.failure, "normal-tools.md")):
            relative = "snapshots/use/" + folder + "/" + filename
            self.assertIn('href="' + relative + '"', page)
            self.assertTrue((moved / relative).is_file())

    def test_missing_linked_or_untrusted_report_paths_do_not_create_links(self):
        path = self.source / self.success / "workflow.md"
        path.unlink()
        path.symlink_to(self.source / self.failure / "normal-tools.md")
        self.progress["import_learning_review"]["failure_example"]["run"] = "reports/harness/../../private"
        page = report.render(self.source, self.progress)
        self.assertEqual(page.count("Readable report not recorded in this snapshot."), 2)
        self.assertNotIn('href="' + self.success + '/workflow.md"', page)
        self.assertNotIn('href="reports/harness/../../private', page)


class RecoveryReportTests(RepairFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.enterContext(patch("recovery.REPORTS", self.root))
        self.enterContext(patch("recover.REPORTS", self.root))
        destination = self.root / "recovery/runs" / self.run.path.name
        destination.parent.mkdir(parents=True)
        self.run.path.rename(destination)
        self.run.path = destination
        self.journal = Journal(self.run, self.checkout)
        self.tools = RecoveryTools(self.run, self.journal)
        self.progress = dict(json.loads((HERE / "progress.json").read_text()), stage="Recover")

    def complete(self):
        for call in self.tools.prepared_calls("retry"):
            self.tools.dispatch(call["name"], call["args"])
        self.tools.finish()
        finalize(self.run, self.journal)

    def test_recovery_report_explains_pass_failed_attempt_and_model_scope(self):
        self.complete()
        page = report.render(self.root, self.progress)
        self.assertIn("continue safely after interruption", page)
        self.assertIn("Recovered repair passed all required checks", page)
        self.assertIn("Repair 1: repair failed its required checks", page)
        self.assertIn("Repair 2: passed every required check", page)
        self.assertIn("No AI model ran", page)
        self.assertIn("no multi-file rollback", page)
        self.assertNotIn('href="#sandbox"', page)
        self.assertIn("shared glossary and file guide #111", page)

    def test_saved_recovery_journal_and_receipts_survive_moving_the_report(self):
        self.complete()
        reports = self.root / "saved"
        report.save_stage(reports, self.progress, self.root, source_ref="a" * 40)
        snapshot = reports / "snapshots/recover"
        directory = snapshot / "recovery/runs" / self.run.path.name
        self.assertEqual((directory / "journal.json").read_bytes(), (self.run.path / "journal.json").read_bytes())
        shutil.rmtree(self.root / "recovery")
        shutil.rmtree(self.root / "verification")
        moved = self.root / "moved"
        reports.rename(moved)
        page = report.render_saved(moved, "recover").read_text()
        self.assertIn("Recovered repair passed all required checks", page)
        embedded = json.loads(report.embedded_evidence(moved / "snapshots/recover", moved))
        entry = embedded[f"snapshots/recover/recovery/runs/{self.run.path.name}/result.json"]
        self.assertIn('"operations"', entry["files"]["journal.json"]["text"])
        self.assertIn('<option value="partial.patch">Observed partial patch</option>', page)

    def test_misleading_handoff_and_changed_child_evidence_cannot_show_a_pass(self):
        self.complete()
        (self.run.path / "handoff.json").write_text('{"last_observed_check":"All checks passed"}')
        item = self.run.result["attempts"][-1]
        child = self.root / "verification/runs" / item["verification_run_id"]
        (child / "candidate.stdout").write_text("Changed evidence")
        row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("Evidence needs inspection", row)
        self.assertNotIn("Recovered repair passed", row)
        self.assertNotIn("All checks passed", row)

    def test_later_reconstruction_stop_and_malformed_journal_preserve_historical_context(self):
        self.complete()
        (self.run.path / "recovery-stop.json").write_text('{"outcome":"incomplete"}')
        row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("A later resume was refused", row)
        self.assertNotIn("Recovered repair passed", row)
        (self.run.path / "recovery-stop.json").unlink()
        (self.run.path / "journal.json").write_text('{"operations":null}')
        row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("could not be reconciled", row)
        self.assertNotIn("Recovered repair passed", row)
        (self.run.path / "journal.json").unlink()
        external = self.root / "external-journal.json"
        external.write_text(json.dumps(self.journal.state))
        (self.run.path / "journal.json").symlink_to(external)
        row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("could not be reconciled", row)
        self.assertNotIn("Recovered repair passed", row)

    def test_live_and_simulated_limits_have_different_explanations_and_escape_observations(self):
        self.run.result.update(candidate_source="AI", provider="openai", model="test-model", model_requests_dispatched=2)
        self.journal.record("interruption_injected", location="</script><img src=x>")
        row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("Live model: openai / test-model", row)
        self.assertNotIn("<img src=x>", row)
        self.run.result["demonstration"] = "simulated-model-limit-real-checks"
        row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
        self.assertIn("model-request limit was simulated", row)
        self.assertIn("No new AI model ran", row)

    def test_malformed_top_level_journal_fields_cannot_receive_a_green_label(self):
        self.complete()
        saved = (self.run.path / "journal.json").read_text()
        for field in ("requests", "records", "resumes", "implementation_sha256"):
            with self.subTest(field=field):
                state = json.loads(saved)
                state[field] = [] if field == "implementation_sha256" else {}
                (self.run.path / "journal.json").write_text(json.dumps(state))
                row = report.render_recovery(self.run.path / "result.json", self.root, self.run.result)
                self.assertIn("Evidence needs inspection", row)
                self.assertNotIn("Recovered repair passed", row)

    def test_linked_check_output_is_not_trusted_or_read_by_saved_reports(self):
        self.complete()
        child = self.root / "verification/runs" / self.run.result["attempts"][-1]["verification_run_id"]
        output = child / "candidate.stdout"
        external = self.root / "external.stdout"
        external.write_bytes(output.read_bytes())
        output.unlink()
        output.symlink_to(external)
        reports = self.root / "saved"
        report.save_stage(reports, self.progress, self.root, source_ref="a" * 40)
        read_bytes = Path.read_bytes

        def guarded(path):
            if path.name == "candidate.stdout" and path.is_symlink():
                self.fail("Report followed linked check output")
            return read_bytes(path)

        with patch.object(Path, "read_bytes", guarded):
            for remove in (False, True):
                if remove:
                    external.unlink()
                page = report.render_saved(reports, "recover").read_text()
                self.assertIn("Evidence needs inspection", page)
                self.assertNotIn("Recovered repair passed all required checks", page)

    def test_invalid_saved_model_settings_do_not_abort_report_publication(self):
        self.complete()
        config = {"provider": "openai", "model": "test-model", "auth": "subscription",
                  "maxModelRequests": 0, "maxOutputTokensPerRequest": None}
        self.run.result["live_config"] = config
        self.journal.state["live_config"] = config
        self.journal.save()
        reports = self.root / "saved"
        report.save_stage(reports, self.progress, self.root, source_ref="a" * 40)
        page = report.render_saved(reports, "recover").read_text()
        self.assertIn("Evidence needs inspection", page)
        self.assertNotIn("Recovered repair passed all required checks", page)


class StageReportTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.reports = self.root / "reports"
        self.progress = dict(json.loads((HERE / "progress.json").read_text()), stage="Connect")

    def source(self, name):
        source = self.root / name
        directory = source / "runs" / name
        directory.mkdir(parents=True)
        (directory / "result.json").write_text(json.dumps({
            "schema_version": 1, "candidate": "good", "outcome": "calibration_passed",
            "checks": [{"id": "baseline", "outcome": "passed", "exit_code": 0},
                       {"id": "seed", "outcome": "assertion_failed", "exit_code": 1},
                       {"id": "candidate", "outcome": "passed", "exit_code": 0}],
        }))
        (directory / "baseline.stdout").write_text("original test output")
        return source

    def restore_define(self):
        source = self.source("define-example")
        progress = dict(self.progress, stage="Define")
        report.save_stage(self.reports, progress, source, source_ref="a" * 40, restored=True)
        return source, report.render_saved(self.reports, "define")

    def test_pi_receipts_keep_prepared_responses_distinct_and_embed_context(self):
        source = self.source("connect-example")
        directory = source / "pi/runs/scripted"
        directory.mkdir(parents=True)
        receipt = json.loads((source / "runs/connect-example/result.json").read_text())
        receipt.update(workflow="pi-scripted-tools", outcome="scripted_loop_passed", startup_verified=True, tool_calls=6)
        (directory / "result.json").write_text(json.dumps(receipt))
        (directory / "context.json").write_text('{"prompt":"Prepared only"}')
        (directory / "pi-session.json").write_text('{"modelCalls":0}')
        report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
        page = report.render_saved(self.reports, "connect").read_text()
        self.assertIn("Pi tools: prepared working repair", page)
        self.assertIn("No AI model ran.", page)
        self.assertIn("Model: none (prepared responses). Tokens used: 0.", page)
        self.assertIn("Prepared only", page)
        self.assertIn('<option value="pi-session.json">Pi conversation</option>', page)
        self.assertTrue((self.reports / "snapshots/connect/pi/runs/scripted/context.json").is_file())
        cancelled = source / "pi/runs/cancelled"
        cancelled.mkdir()
        (cancelled / "result.json").write_text(json.dumps({**receipt, "outcome": "cancelled"}))
        page = report.render(source, self.progress)
        self.assertIn("Pi tools: interrupted run", page)
        self.assertIn("The real Pi tool loop completed using prepared responses.", page)
        receipt["checks"].pop()
        (directory / "result.json").write_text(json.dumps(receipt))
        result = report.render_run(directory / "result.json", source, receipt)
        self.assertNotIn("As expected", result)

    def test_connect_refresh_preserves_define_page_and_saved_inputs(self):
        _, page = self.restore_define()
        originals = {path: path.read_bytes() for path in self.reports.rglob("*") if path.is_file()}
        source = self.source("connect-example")
        report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
        report.render_saved(self.reports, "connect")
        (source / "runs/connect-example/result.json").write_text("incomplete update")
        report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
        report.render_saved(self.reports, "connect")
        for path, content in originals.items():
            self.assertEqual(path.read_bytes(), content, str(path))
        self.assertNotIn('id="sandbox"', page.read_text())
        self.assertIn("without AI or containers", page.read_text())
        self.assertIn("Historical checkpoint", page.read_text())

    def test_cancelled_annotations_require_matching_run_and_confirmed_cleanup(self):
        source = self.source("cancelled-example")
        path = source / "runs/cancelled-example/result.json"
        receipt = json.loads(path.read_text())
        receipt.update(outcome="cancelled", workflow="pi-scripted-tools", model_requests=0, tool_calls=0,
                       containers=[{"removed": True}])
        annotation = {"kind": "cancellation-test", "title": "Deliberate cancellation",
                      "label": "Cleanup confirmed", "summary": "Stopped deliberately; cleanup was confirmed.",
                      "evidence": "saved-checkpoint.json"}
        page = report.render_run(path, source, receipt)
        self.assertIn("Run cancelled", page)
        self.assertNotIn("Cleanup confirmed", page)
        page = report.render_run(path, source, receipt, annotation=annotation)
        self.assertIn("Cleanup confirmed", page)
        self.assertIn("original run outcome remains cancelled", page)
        self.assertEqual(receipt["outcome"], "cancelled")
        receipt["containers"][0]["removed"] = False
        page = report.render_run(path, source, receipt, annotation=annotation)
        self.assertNotIn("Cleanup confirmed", page)
        self.assertIn("Run cancelled", page)
        receipt["containers"][0]["removed"] = True
        receipt["workflow"] = "pi-live-tools"
        annotation.update(kind="fixed-startup-bug", title="Earlier startup bug", label="Startup bug fixed")
        page = report.render_run(path, source, receipt, annotation=annotation)
        self.assertIn("Startup bug fixed", page)
        receipt["model_requests"] = 1
        page = report.render_run(path, source, receipt, annotation=annotation)
        self.assertNotIn("Startup bug fixed", page)
        self.assertIn("Run cancelled", page)

    def test_live_report_names_model_and_requires_actual_startup_and_check_evidence(self):
        source = self.source("live-example")
        path = source / "runs/live-example/result.json"
        receipt = json.loads(path.read_text())
        receipt.update(candidate="model", workflow="pi-live-tools", outcome="live_repair_passed",
                       provider="openai", model="gpt-6.1-sol", startup_verified=True, model_requests=3,
                       model_usage=[{"usage": {"output": 42}}])
        page = report.render_run(path, source, receipt)
        self.assertIn("AI-written repair passed", page)
        self.assertIn("openai / gpt-6.1-sol", page)
        self.assertIn("3 model request attempts", page)
        self.assertIn("Recorded output tokens: 42", page)
        self.assertIn("Input and total token usage were not reported", page)
        self.assertIn("As expected", page)
        receipt["token_summary"] = {"totals": {"inputTokens": 100, "cachedInputTokens": 20,
                                   "outputTokens": 50, "reasoningTokens": 10, "totalTokens": 150},
                                   "requests_with_usage": 2, "requests_without_usage": 1, "complete": False,
                                   "largest_response_output_tokens": 30, "responses_over_4096_output_tokens": 0}
        receipt["provider_usage"] = [{"request": 1, "usage": {"inputTokens": 70, "outputTokens": 30, "totalTokens": 100}},
                                    {"request": 1, "usage": {"inputTokens": 70, "outputTokens": 30, "totalTokens": 100}},
                                    {"request": 2, "usage": {"inputTokens": 30, "outputTokens": 20, "totalTokens": 50}}]
        page = report.render_run(path, source, receipt)
        self.assertIn("Recorded tokens: 150", page)
        self.assertIn("100 input + 50 output", page)
        self.assertIn("included in input", page)
        self.assertIn("included in output", page)
        self.assertIn("totals are partial", page)
        self.assertIn("Partial usage record", page)
        self.assertIn("Usage per request: openai / gpt-6.1-sol", page)
        self.assertEqual(page.count('Request 1: <strong>100 tokens</strong> (70 input + 30 output)'), 1)
        self.assertIn('Request 2: <strong>50 tokens</strong> (30 input + 20 output)', page)
        receipt["startup_verified"] = False
        page = report.render_run(path, source, receipt)
        self.assertNotIn("As expected", page)
        self.assertIn("Unreadable record", page)

    def test_live_token_report_distinguishes_missing_usage_from_no_model_requests(self):
        source = self.source("missing-usage")
        path = source / "runs/missing-usage/result.json"
        receipt = {"schema_version": 1, "workflow": "pi-live-tools", "outcome": "cancelled",
                   "provider": "openai", "model": "gpt-6-luna", "model_requests": 1,
                   "model_usage": [{"usage": {"output": 0}}]}
        page = report.render_run(path, source, receipt)
        self.assertIn("openai / gpt-6-luna", page)
        self.assertIn("Token usage: not reported", page)
        self.assertNotIn("Tokens used: 0", page)
        receipt["model_requests"] = 0
        page = report.render_run(path, source, receipt)
        self.assertIn("Tokens used: 0. No model requests were made", page)

    def test_saved_evidence_survives_source_removal_and_bundle_move(self):
        source, _ = self.restore_define()
        shutil.rmtree(source)
        moved = self.root / "moved"
        shutil.copytree(self.reports, moved)
        page = report.render_saved(moved, "define").read_text()
        relative = "snapshots/define/runs/define-example/result.json"
        self.assertIn(f'href="{relative}"', page)
        self.assertTrue((moved / relative).is_file())
        self.assertEqual((moved / relative).with_name("baseline.stdout").read_text(), "original test output")
        self.assertIn("The proposed repair passed this check.", page)

    def test_restore_rejects_missing_evidence_and_existing_snapshot(self):
        with self.assertRaisesRegex(ValueError, "no saved receipts"):
            report.save_stage(self.reports, dict(self.progress, stage="Define"), self.root / "missing",
                              source_ref="a" * 40, restored=True)
        source, _ = self.restore_define()
        with self.assertRaisesRegex(ValueError, "already exists"):
            report.save_stage(self.reports, dict(self.progress, stage="Define"), source,
                              source_ref="a" * 40, restored=True)

    def test_failed_restoration_leaves_no_partial_snapshot_and_can_be_retried(self):
        source = self.source("restore-retry")
        progress = dict(self.progress, stage="Define")
        copytree = shutil.copytree

        def interrupted_copy(*args, **kwargs):
            copytree(*args, **kwargs)
            raise OSError("Copy interrupted after writing evidence")

        with patch("report.shutil.copytree", side_effect=interrupted_copy), self.assertRaises(OSError):
            report.save_stage(self.reports, progress, source, source_ref="a" * 40, restored=True)
        self.assertFalse((self.reports / "snapshots/define").exists())
        self.assertEqual(list((self.reports / "snapshots").iterdir()), [])
        report.save_stage(self.reports, progress, source, source_ref="a" * 40, restored=True)
        self.assertIn("Historical checkpoint", report.render_saved(self.reports, "define").read_text())

    def test_capture_rejects_linked_receipts_and_run_directories_before_opening(self):
        for linked in ("receipt", "run-directory"):
            with self.subTest(linked=linked):
                source = self.source("linked-" + linked)
                run = source / "runs" / ("linked-" + linked)
                path = run / "result.json" if linked == "receipt" else run
                outside = self.root / ("outside-" + linked)
                path.rename(outside)
                path.symlink_to(outside, target_is_directory=linked == "run-directory")
                with patch("report.os.open") as opened, patch("report.shutil.copytree") as copied, \
                        self.assertRaisesRegex(ValueError, "symlink"):
                    report.save_stage(self.reports, dict(self.progress, stage="Define"), source,
                                      source_ref="a" * 40, restored=True)
                opened.assert_not_called()
                copied.assert_not_called()
                self.assertFalse((self.reports / "snapshots/define").exists())

    def test_failed_recapture_preserves_complete_previous_snapshot_and_page(self):
        source = self.source("recapture")
        report.save_stage(self.reports, self.progress, source, source_ref="a" * 40)
        report.render_saved(self.reports, "connect")
        originals = {path: path.read_bytes() for path in self.reports.rglob("*") if path.is_file()}
        (source / "runs/recapture/baseline.stdout").write_text("new test output")
        copytree = shutil.copytree
        write_json = report.write_json

        def interrupted_copy(*args, **kwargs):
            copytree(*args, **kwargs)
            raise OSError("Copy interrupted after writing evidence")

        def interrupted_manifest(path, value):
            if path.name == "snapshot.json":
                raise OSError("Cannot write manifest")
            write_json(path, value)

        failures = (patch("report.shutil.copytree", side_effect=interrupted_copy),
                    patch("report.write_json", side_effect=interrupted_manifest),
                    patch("report.publish_snapshot", side_effect=OSError("Exchange unavailable")))
        for failure in failures:
            with failure, self.assertRaises(OSError):
                report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
            self.assertEqual({path: path.read_bytes() for path in self.reports.rglob("*") if path.is_file()}, originals)
            self.assertEqual(list((self.reports / "snapshots").glob(".connect-*")), [])
        report.render_saved(self.reports, "connect")
        report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
        saved = self.reports / "snapshots/connect"
        self.assertEqual((saved / "runs/recapture/baseline.stdout").read_text(), "new test output")
        self.assertEqual(json.loads((saved / "snapshot.json").read_text())["source_ref"], "b" * 40)
        report.render_saved(self.reports, "connect")

    def test_recapture_keeps_saved_runs_when_original_runs_are_removed(self):
        source = self.source("retained")
        report.save_stage(self.reports, self.progress, source, source_ref="a" * 40)
        shutil.rmtree(source)
        source = self.source("new-run")
        report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
        saved = self.reports / "snapshots/connect"
        for name in ("retained", "new-run"):
            self.assertTrue((saved / "runs" / name / "result.json").is_file())
        report.render_saved(self.reports, "connect")

    def test_unsupported_atomic_exchange_preserves_both_directories(self):
        staging, saved = self.root / "staging", self.root / "saved"
        for path, content in ((staging, "new"), (saved, "old")):
            path.mkdir()
            (path / "evidence").write_text(content)
        for library in (Mock(spec=[]), Mock(renameat2=Mock(return_value=-1))):
            with patch("report.ctypes.CDLL", return_value=library), \
                    patch("report.ctypes.get_errno", return_value=errno.EOPNOTSUPP), self.assertRaises(OSError):
                report.publish_snapshot(staging, saved)
            self.assertEqual((staging / "evidence").read_text(), "new")
            self.assertEqual((saved / "evidence").read_text(), "old")

    def test_refresh_rejects_linked_receipt_before_hashing_external_content(self):
        source, page = self.restore_define()
        original_page = page.read_bytes()
        receipt = self.reports / "snapshots/define/runs/define-example/result.json"
        receipt.unlink()
        receipt.symlink_to(source / "runs/define-example/result.json")
        with patch("report.os.open") as opened, self.assertRaisesRegex(ValueError, "symlink"):
            report.render_saved(self.reports, "define")
        opened.assert_not_called()
        self.assertEqual(page.read_bytes(), original_page)

    def test_evidence_changed_during_rendering_does_not_replace_report(self):
        source = self.source("concurrent-change")
        (source / "runs/concurrent-change/candidate.patch").write_text("Original patch")
        report.save_stage(self.reports, dict(self.progress, stage="Define"), source,
                          source_ref="a" * 40, restored=True)
        page = report.render_saved(self.reports, "define")
        original_page = page.read_bytes()
        render = report.render

        def changed_render(saved, *args, **kwargs):
            (saved / "runs/concurrent-change/candidate.patch").write_text("Changed during rendering")
            return render(saved, *args, **kwargs)

        with patch("report.render", side_effect=changed_render), self.assertRaisesRegex(ValueError, "changed during rendering"):
            report.render_saved(self.reports, "define")
        self.assertEqual(page.read_bytes(), original_page)
        self.assertEqual(list(self.reports.glob(".define-render-*")), [])

    def test_failed_html_write_preserves_previous_report(self):
        _, page = self.restore_define()
        original_page = page.read_bytes()
        with patch.object(Path, "write_text", side_effect=OSError("Disk full")), self.assertRaises(OSError):
            report.render_saved(self.reports, "define")
        self.assertEqual(page.read_bytes(), original_page)
        self.assertEqual(list(self.reports.glob(".define-render-*")), [])

    def test_saved_hash_and_inventory_mismatches_do_not_overwrite_existing_report(self):
        for changed in ("progress", "receipt", "missing", "extra"):
            with self.subTest(changed=changed):
                reports = self.root / changed
                source = self.source("snapshot-" + changed)
                report.save_stage(reports, dict(self.progress, stage="Define"), source, source_ref="a" * 40, restored=True)
                page = report.render_saved(reports, "define")
                original = page.read_bytes()
                snapshot = reports / "snapshots/define"
                receipt = next(snapshot.glob("runs/*/result.json"))
                if changed == "progress":
                    (snapshot / "progress.json").write_text(json.dumps(dict(self.progress, stage="Define", status="reviewed")))
                elif changed == "receipt":
                    receipt.write_text(json.dumps({"schema_version": 1, "outcome": "calibration_passed", "checks": []}))
                elif changed == "missing":
                    receipt.unlink()
                else:
                    extra = snapshot / "runs/extra/result.json"
                    extra.parent.mkdir()
                    extra.write_text("{}")
                with self.assertRaisesRegex(ValueError, "snapshot"):
                    report.render_saved(reports, "define")
                self.assertEqual(page.read_bytes(), original)

    def test_expected_repair_failures_require_all_three_consistent_checks(self):
        source = self.source("expected-failure")
        path = source / "runs/expected-failure/result.json"
        receipt = json.loads(path.read_text())
        receipt.update(outcome="repair_failed", candidate="wrong")
        receipt["checks"][-1].update(outcome="assertion_failed", exit_code=1)
        for candidate in ("wrong", "empty"):
            receipt["candidate"] = candidate
            self.assertIn("As expected", report.render_run(path, source, receipt))
            checks = receipt["checks"]
            invalid_cases = [[], checks[:-1], list(reversed(checks)), checks + checks[-1:],
                             [dict(check, exit_code=0) for check in checks],
                             [*checks[:-1], dict(checks[-1], outcome="passed", exit_code=0)],
                             [*checks[:-1], dict(checks[-1], exit_code=None)]]
            for invalid in invalid_cases:
                with self.subTest(candidate=candidate, checks=invalid):
                    page = report.render_run(path, source, dict(receipt, checks=invalid))
                    self.assertNotIn("As expected", page)
                    self.assertNotIn("failure is the expected answer", page)
                    self.assertIn("Unreadable record", page)

    def test_every_copied_evidence_file_is_sealed_before_refresh(self):
        names = (*report.EVIDENCE_FILES, "baseline.stdout", "snapshot.stdout")
        for name in names:
            for change in ("modified", "removed"):
                with self.subTest(name=name, change=change):
                    identifier = name + "-" + change
                    reports = self.root / ("reports-" + identifier)
                    source = self.source(identifier)
                    original_file = source / "runs" / identifier / name
                    if name != "result.json":
                        original_file.write_bytes(b"original evidence\xff")
                    report.save_stage(reports, dict(self.progress, stage="Define"), source,
                                      source_ref="a" * 40, restored=True)
                    page = report.render_saved(reports, "define")
                    original_page = page.read_bytes()
                    copied = reports / "snapshots/define/runs" / identifier / name
                    if change == "modified":
                        copied.write_bytes(b"changed evidence")
                    else:
                        copied.unlink()
                    with self.assertRaisesRegex(ValueError, "snapshot"):
                        report.render_saved(reports, "define")
                    self.assertEqual(page.read_bytes(), original_page)

    def test_added_evidence_and_file_replaced_with_symlink_are_rejected(self):
        source, page = self.restore_define()
        snapshot = self.reports / "snapshots/define"
        copied = snapshot / "runs/define-example/baseline.stdout"
        original_page = page.read_bytes()
        extra = copied.with_name("candidate.patch")
        extra.write_text("Added after capture")
        with self.assertRaisesRegex(ValueError, "snapshot"):
            report.render_saved(self.reports, "define")
        extra.unlink()
        copied.unlink()
        copied.symlink_to(source / "runs/define-example/baseline.stdout")
        with self.assertRaisesRegex(ValueError, "snapshot"):
            report.render_saved(self.reports, "define")
        self.assertEqual(page.read_bytes(), original_page)

    def test_symlink_targets_are_recorded_without_reading_their_contents(self):
        source = self.source("linked-evidence")
        outside = self.root / "outside.jsonl"
        outside.write_text("private original")
        linked = source / "runs/linked-evidence/events.jsonl"
        linked.symlink_to(outside)
        linked.with_name("external-directory").symlink_to(self.root / "unavailable-directory", target_is_directory=True)
        report.save_stage(self.reports, self.progress, source, source_ref="a" * 40)
        snapshot = self.reports / "snapshots/connect"
        inventory = json.loads((snapshot / "snapshot.json").read_text())["evidence_inventory"]
        self.assertEqual(inventory["runs/linked-evidence/events.jsonl"], {"kind": "symlink", "target": str(outside)})
        outside.write_text("private changed")
        page = report.render_saved(self.reports, "connect")
        self.assertNotIn("private changed", page.read_text())
        copied = snapshot / "runs/linked-evidence/events.jsonl"
        copied.unlink()
        copied.symlink_to(self.root / "different-target")
        with self.assertRaisesRegex(ValueError, "snapshot"):
            report.render_saved(self.reports, "connect")

    def test_legacy_snapshot_without_evidence_inventory_cannot_silently_refresh(self):
        _, page = self.restore_define()
        original_page = page.read_bytes()
        path = self.reports / "snapshots/define/snapshot.json"
        snapshot = json.loads(path.read_text())
        del snapshot["evidence_inventory"]
        path.write_text(json.dumps(snapshot))
        with self.assertRaisesRegex(ValueError, "no complete evidence inventory"):
            report.render_saved(self.reports, "define")
        self.assertEqual(page.read_bytes(), original_page)

    def test_build_failure_remains_a_failed_repair_without_expected_failure_badge(self):
        source = self.source("build-failed-repair")
        path = source / "runs/build-failed-repair/result.json"
        receipt = json.loads(path.read_text())
        receipt.update(outcome="repair_failed")
        receipt["checks"][-1].update(outcome="build_failed", exit_code=1)
        for candidate in ("model", "wrong", "empty"):
            with self.subTest(candidate=candidate):
                page = report.render_run(path, source, dict(receipt, candidate=candidate))
                self.assertIn("Repair check failed", page)
                self.assertIn("code could not be built", page)
                self.assertNotIn("Unreadable record", page)
                self.assertNotIn("As expected", page)

    def test_style_refresh_does_not_recapture_progress_or_evidence(self):
        _, _ = self.restore_define()
        snapshot = self.reports / "snapshots/define"
        originals = {path: path.read_bytes() for path in snapshot.rglob("*") if path.is_file()}
        with patch("report.REPORTS", self.reports), patch("sys.argv", ["report.py", "--refresh-saved"]):
            report.main()
        for path, content in originals.items():
            self.assertEqual(path.read_bytes(), content)

    def test_failed_index_publication_preserves_previous_index_and_allows_retry(self):
        source = self.source("index-example")
        progress = json.loads((HERE / "progress.json").read_text())
        report.save_stage(self.reports, progress, source, source_ref="a" * 40)
        target = self.reports / "index.html"
        target.write_bytes(b"Previous complete index")
        write_bytes, replace = Path.write_bytes, Path.replace

        for failure in ("copy", "replace"):
            def failed_write(path, content):
                if path.name == "index.html" and failure == "copy":
                    write_bytes(path, content[:10])
                    raise OSError("Disk full during index copy")
                return write_bytes(path, content)

            def failed_replace(path, destination):
                if path.name == "index.html":
                    raise OSError("Cannot publish index")
                return replace(path, destination)

            with self.subTest(failure=failure), patch("report.REPORTS", self.reports), \
                    patch("sys.argv", ["report.py", "--refresh-saved"]), \
                    patch.object(Path, "write_bytes", failed_write), \
                    patch.object(Path, "replace", failed_replace), self.assertRaises(SystemExit) as caught:
                report.main()
            self.assertEqual(caught.exception.code, 2)
            self.assertEqual(target.read_bytes(), b"Previous complete index")
            self.assertEqual(list(self.reports.glob(".index-render-*")), [])

        with patch("report.REPORTS", self.reports), patch("sys.argv", ["report.py", "--refresh-saved"]):
            report.main()
        self.assertEqual(target.read_bytes(), (self.reports / (progress["stage"].lower() + ".html")).read_bytes())
        self.assertEqual(list(self.reports.glob(".index-render-*")), [])

    def test_embedded_evidence_roundtrips_text_without_executable_html(self):
        source = self.source("unsafe-text")
        directory = source / "runs/unsafe-text"
        payload = '</script><img src=x onerror="alert(1)">\r\n+changed line\r\n'
        (directory / "candidate.patch").write_bytes(payload.encode())
        (directory / "snapshot.stdout").write_text("host archive must not be embedded")
        embedded = report.embedded_evidence(source)
        self.assertNotIn("<", embedded)
        self.assertNotIn("host archive must not be embedded", embedded)
        files = json.loads(embedded)["runs/unsafe-text/result.json"]["files"]
        self.assertEqual(files["candidate.patch"]["text"], payload)
        self.assertEqual(set(files), set(report.EVIDENCE_FILES))

    def test_embedded_evidence_distinguishes_missing_empty_unreadable_and_linked_files(self):
        source = self.source("file-states")
        directory = source / "runs/file-states"
        (directory / "candidate.patch").write_text("")
        (directory / "seed.patch").write_bytes(b"\xff")
        outside = self.root / "outside.txt"
        outside.write_text("outside content must not be embedded")
        (directory / "events.jsonl").symlink_to(outside)
        embedded = report.embedded_evidence(source)
        self.assertNotIn("outside content must not be embedded", embedded)
        files = json.loads(embedded)["runs/file-states/result.json"]["files"]
        self.assertEqual(files["candidate.patch"]["text"], "")
        self.assertEqual(files["task.json"]["unavailable"], "Not recorded for this run.")
        self.assertIn("UTF-8", files["seed.patch"]["unavailable"])
        self.assertEqual(files["events.jsonl"]["unavailable"], "Linked files are not embedded.")
        report.save_stage(self.reports, self.progress, source, source_ref="b" * 40)
        archived = report.embedded_evidence(self.reports / "snapshots/connect")
        self.assertNotIn("outside content must not be embedded", archived)
        self.assertEqual(json.loads(archived)["runs/file-states/result.json"]["files"]["events.jsonl"]["text"], None)


if __name__ == "__main__":
    unittest.main()
