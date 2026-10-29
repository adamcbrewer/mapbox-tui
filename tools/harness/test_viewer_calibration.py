import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from viewer_probe import observation, probe


class ViewerCalibrationTests(unittest.TestCase):
    def setUp(self):
        ref = {"document_id": 1, "item_id": 1}
        self.operation = {"state": "applied", "changed": True, "result": {"ref": ref}}
        self.status = {"document_count": 1, "active_item": ref,
                       "tracks": [{"items": [{"point_count": 2, "visible": True, "ref": ref}]}]}

    def test_success_requires_observed_import_not_just_applied_acknowledgement(self):
        self.assertEqual(observation(self.operation, self.status, True), "passed")
        for field, value in (("document_count", 0), ("active_item", None), ("tracks", [])):
            status = {**self.status, field: value}
            self.assertEqual(observation(self.operation, status, True), "unexpected_state")
        status = copy.deepcopy(self.status)
        status["tracks"][0]["items"][0]["point_count"] = 1
        self.assertEqual(observation(self.operation, status, True), "unexpected_state")

    def test_pending_is_only_the_selected_regression_with_modal_and_empty_workspace(self):
        pending = {"state": "pending"}
        empty = {"document_count": 0}
        self.assertEqual(observation(pending, empty, True), "modal_import_stalled")
        self.assertEqual(observation(pending, empty, False), "blocked")
        self.assertEqual(observation(pending, self.status, True), "unexpected_state")
        self.assertEqual(observation({"state": "failed"}, empty, True), "unexpected_state")

    def test_unavailable_terminal_stops_before_launch_and_retains_blocker(self):
        with tempfile.TemporaryDirectory() as folder:
            binary = Path(folder) / "trusted-viewer"
            binary.write_bytes(b"not executed")
            output = Path(folder) / "evidence"
            with patch("viewer_probe.preflight", side_effect=FileNotFoundError("Missing terminal prerequisite: tmux")), \
                    patch("viewer_probe.terminal.tmux") as launch:
                result = probe(binary, output)
            launch.assert_not_called()
            self.assertEqual(result["outcome"], "blocked")
            self.assertFalse(result["cleanup"])
            self.assertIn("Missing terminal prerequisite", result["reason"])
            self.assertEqual(json.loads((output / "result.json").read_text()), result)


class ViewerArtifactTests(unittest.TestCase):
    def stream(self, name="commands/after-import.png", data="aW1hZ2U="):
        marker = "HARNESS_VIEWER_ARTIFACT " + json.dumps({"name": name, "data": data}) + "\n"
        return b"\n".join(json.dumps({"Output": chunk}).encode() for chunk in (marker[:40], marker[40:]))

    def test_split_go_log_lines_are_reassembled_without_executing_content(self):
        from container import retain_viewer_artifacts
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            result = retain_viewer_artifacts(self.stream(), root)
            self.assertEqual((root / "commands/after-import.png").read_bytes(), b"image")
            self.assertIn("commands/after-import.png", result)

    def test_capture_names_cannot_escape_the_evidence_directory(self):
        from container import retain_viewer_artifacts
        from run import Stop
        with tempfile.TemporaryDirectory() as folder:
            for name in ("../outside.png", "/tmp/outside.png", "commands/../../outside.png", "commands/image.html"):
                with self.subTest(name=name), self.assertRaises(Stop):
                    retain_viewer_artifacts(self.stream(name), Path(folder))
            with self.assertRaises(Stop):
                retain_viewer_artifacts(self.stream(data="not base64"), Path(folder))
            with self.assertRaises(Stop):
                retain_viewer_artifacts(self.stream() + b"\n" + self.stream(), Path(folder))

    def test_environment_failure_cannot_be_calibrated_as_the_expected_bug(self):
        from run import classify_test
        output = b'{"Action":"output","Output":"HARNESS_VIEWER_BLOCKED: tmux missing"}\n{"Action":"fail","Test":"Viewer"}'
        self.assertEqual(classify_test(output, 1, {"test": "Viewer", "cases": []}), "blocked")
