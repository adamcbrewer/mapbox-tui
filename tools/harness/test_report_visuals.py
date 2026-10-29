import copy
from pathlib import Path
import tempfile
import unittest

import report_visuals
from test_measurements import sample_inventory


class ReportVisualTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.value, _ = sample_inventory(Path(temporary.name))

    def test_limited_pass_is_not_a_fully_checked_fix(self):
        row = copy.deepcopy(self.value["records"][0])
        row.update(outcome="live_repair_passed", verified_success=False, unconfirmed_completion=False, mode="live")
        self.assertEqual(report_visuals.outcome(row), "focused")
        page = report_visuals.overview(dict(records=[row]))
        live = page.split('id="overview-live"', 1)[1].split('id="overview-prepared"', 1)[0]
        self.assertIn('data-stat="1">0</span>', live)
        self.assertIn("Passed a limited check", live)

    def test_live_demo_and_child_receipts_never_mix_in_summary_counts(self):
        live = copy.deepcopy(self.value["records"][0])
        live.update(mode="live", is_child=False, duplicate=False)
        child = dict(live, is_child=True)
        replay = dict(live, duplicate=True)
        value = dict(records=[*self.value["records"], live, child, replay])
        page = report_visuals.overview(value)
        self.assertIn('AI task attempts <span>1</span>', page)
        self.assertIn('Prepared test examples <span>6</span>', page)

    def test_partial_billing_is_not_presented_as_a_total_or_zero(self):
        page = report_visuals.overview(self.value)
        prepared = page.split('id="overview-prepared"', 1)[1]
        self.assertIn('data-stat="3">Incomplete</span>', prepared)
        self.assertIn("Complete billing recorded for 3 of 6 attempts", prepared)
        for row in self.value["records"]:
            row["measurement"]["metrics"]["total_cost_usd"] = None
        self.assertIn('data-stat="3">Not recorded</span>', report_visuals.overview(self.value))

    def test_human_labels_and_times_stay_readable_and_inert(self):
        row = copy.deepcopy(self.value["records"][0])
        row["pins"]["model"] = "<img src=x onerror=alert(1)>"
        page = report_visuals.overview(dict(records=[row]))
        self.assertNotIn("<img", page)
        self.assertIn("&lt;img", page)
        self.assertEqual(report_visuals.runtime(None), "Not recorded")
        self.assertEqual(report_visuals.runtime(97.39425), "1m 37s")
        self.assertEqual(report_visuals.runtime(0), "0s")


if __name__ == "__main__":
    unittest.main()
