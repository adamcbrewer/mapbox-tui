import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

import report
from run import HERE


class StageReportTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.reports = self.root / "reports"
        self.progress = json.loads((HERE / "progress.json").read_text())

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

    def test_style_refresh_does_not_recapture_progress_or_evidence(self):
        _, _ = self.restore_define()
        snapshot = self.reports / "snapshots/define"
        originals = {path: path.read_bytes() for path in snapshot.rglob("*") if path.is_file()}
        with patch("report.REPORTS", self.reports), patch("sys.argv", ["report.py", "--refresh-saved"]):
            report.main()
        for path, content in originals.items():
            self.assertEqual(path.read_bytes(), content)

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
