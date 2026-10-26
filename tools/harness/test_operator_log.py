import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import operator_log as operator
from run import Stop


class OperatorTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        operator.create(self.directory)

    def test_missing_participation_is_unknown_and_supplied_events_are_not_human_time(self):
        result = operator.summary(self.directory)
        self.assertIsNone(result["attention_seconds"])
        self.assertEqual(result["participation"], "not recorded")
        operator.append(self.directory, "monitoring", actor="supplied-test")
        operator.append(self.directory, "finished", actor="supplied-test")
        self.assertIsNone(operator.summary(self.directory)["attention_seconds"])

    def test_explicit_intervals_separate_attention_waiting_and_breaks(self):
        for when, action in ((10, "monitoring"), (15, "away"), (35, "paused"), (40, "reviewing"), (47, "finished")):
            with patch("operator_log.time.monotonic", return_value=when):
                operator.append(self.directory, action)
        result = operator.summary(self.directory)
        self.assertEqual(result["attention_seconds"], 12)
        self.assertEqual(result["recorded_intervals_seconds"]["away"], 20)
        self.assertEqual(result["recorded_intervals_seconds"]["paused"], 5)
        self.assertTrue(result["timing_complete"])
        self.assertIn("unrecorded attention remain unknown", result["coverage"])

    def test_feedback_is_consumed_once_and_late_feedback_is_refused(self):
        event = operator.append(self.directory, "feedback", "Please check the result")
        self.assertEqual(operator.take_feedback(self.directory)["id"], event["id"])
        self.assertIsNone(operator.take_feedback(self.directory))
        operator.close(self.directory)
        with self.assertRaises(Stop):
            operator.append(self.directory, "feedback", "Too late")
        operator.append(self.directory, "reviewing")
        self.assertEqual(operator.summary(self.directory)["corrections_consumed"], 1)

    def test_stop_cannot_signal_a_reused_process_or_a_finished_run(self):
        with patch("operator_log.os.kill") as kill, patch("operator_log.process_identity", return_value={"boot": "another boot"}):
            operator.append(self.directory, "stop")
        kill.assert_not_called()
        operator.close(self.directory)
        with patch("operator_log.os.kill") as kill:
            operator.append(self.directory, "stop")
        kill.assert_not_called()

    def test_invalid_or_unfinished_timing_does_not_become_a_complete_measurement(self):
        operator.append(self.directory, "monitoring")
        self.assertIsNone(operator.summary(self.directory)["attention_seconds"])
        operator.append(self.directory, "finished")
        path = self.directory / "operator.json"
        record = json.loads(path.read_text())
        record["events"][-1]["boot"] = "another-boot"
        path.write_text(json.dumps(record))
        self.assertFalse(operator.summary(self.directory)["timing_complete"])
        for message in ("", "x" * 2001):
            with self.assertRaises(Stop):
                operator.append(self.directory, "feedback", message)


if __name__ == "__main__":
    unittest.main()
