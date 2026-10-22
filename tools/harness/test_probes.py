import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from container import ContainerRun
import probes
from report import render_probes
from run import Stop


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.image = "sha256:" + "a" * 64

    def test_interruption_only_counts_after_readiness_and_confirmed_removal(self):
        for phase in ("timeout", "cancel"):
            for ready, removed in ((True, True), (False, True), (True, False)):
                reports = self.root / f"{phase}-{ready}-{removed}"
                outcome = "timeout" if phase == "timeout" else "cancelled"

                def interrupted(run, name, cwd, arguments=()):
                    if ready:
                        (run.path / (name + ".stdout")).write_text("probe-ready\n")
                    run.result["containers"].append({"name": "prepared-example", "removed": removed})
                    run.result["checks"].append({"id": name, "outcome": outcome})
                    raise Stop(outcome, "Deliberate stop")

                with self.subTest(phase=phase, ready=ready, removed=removed), \
                     patch("probes.REPORTS", reports), patch("sys.argv", ["probes.py", "--image", self.image, "--phase", phase]), \
                     patch("probes.preflight"), patch.object(ContainerRun, "prepare"), \
                     patch.object(ContainerRun, "probe", interrupted):
                    self.assertEqual(probes.main(), 1)
                receipt = next(reports.glob("sandbox-probes/runs/*/result.json"))
                result = json.loads(receipt.read_text())
                self.assertEqual(result["probe_confirmed"], ready and removed)
                self.assertFalse(result["isolation_demonstrated"])
                page = render_probes(reports)
                self.assertEqual("its container was removed" in page, ready and removed)
                self.assertEqual(result["human_acceptance"], "pending")

    def test_isolation_is_demonstrated_only_by_a_successful_boundary_probe(self):
        for passed in (True, False):
            reports = self.root / str(passed)

            def boundary(run, _checkout):
                if not passed:
                    raise Stop("blocked", "Probe failed")
                run.result.update(outcome="boundary_passed", boundary_probes_passed=True)

            with self.subTest(passed=passed), patch("probes.REPORTS", reports), \
                    patch("sys.argv", ["probes.py", "--image", self.image, "--phase", "boundary"]), \
                    patch("probes.preflight"), patch.object(ContainerRun, "prepare"), \
                    patch("probes.boundary_probe", side_effect=boundary):
                self.assertEqual(probes.main(), 0 if passed else 1)
            receipt = json.loads(next(reports.glob("sandbox-probes/runs/*/result.json")).read_text())
            self.assertEqual(receipt["isolation_demonstrated"], passed)
            self.assertEqual(receipt["probe_confirmed"], passed)

    def test_report_rejects_inconsistent_probe_receipts_and_escapes_failures(self):
        directory = self.root / "sandbox-probes/runs/example"
        directory.mkdir(parents=True)
        path = directory / "result.json"
        result = {"phase": "boundary", "outcome": "boundary_passed", "probe_confirmed": True,
                  "containers": [{"removed": True}], "checks": [], "reason": "<script>untrusted</script>"}
        path.write_text(json.dumps(result))
        page = render_probes(self.root)
        self.assertIn("No confirmed demonstration", page)
        self.assertNotIn("<script>", page)
        result["checks"] = [{"id": "boundary-probe", "exit_code": 0, "outcome": "passed"}]
        path.write_text(json.dumps(result))
        self.assertIn("Dummy host-file/network access was denied", render_probes(self.root))
        result["containers"][0]["removed"] = False
        path.write_text(json.dumps(result))
        self.assertIn("No confirmed demonstration", render_probes(self.root))


if __name__ == "__main__":
    unittest.main()
