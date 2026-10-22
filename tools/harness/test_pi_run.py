import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import pi_run
from pi_run import MAX_CALLS, Tools, pi_loop
from report import render_run
from run import HERE, Run, Stop, digest


class PiToolTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.checkout = self.root / "copy"
        (self.checkout / "go").mkdir(parents=True)
        for file, content in (("import.go", "one\n"), ("import_test.go", "protected\n"),
                              ("import_edge_test.go", "protected\n")):
            (self.checkout / "go" / file).write_text(content)
        self.run = Run(self.root / "reports")
        self.run.task = json.loads((HERE / "task.json").read_text())
        self.run.task["source_sha256"] = {file: digest((self.checkout / file).read_bytes())
                                          for file in self.run.task["source_sha256"]}
        self.tools = Tools(self.run, self.checkout)
        self.edit = {"file": "go/import.go", "operation_id": "one", "expected_sha256": digest(b"one\n"),
                     "old": "one", "new": "two"}

    def test_scope_and_symlink_escape(self):
        for file in ("/etc/passwd", "../go/import.go", "go/../go/import.go", "go/new.go"):
            self.assertEqual(self.tools.dispatch("harness_read", {"file": file})["outcome"], "rejected")
        path = self.checkout / "go/import.go"
        path.unlink()
        path.symlink_to(HERE / "task.json")
        self.assertEqual(self.tools.dispatch("harness_read", {"file": "go/import.go"})["outcome"], "rejected")
        self.assertEqual(self.tools.dispatch("harness_edit", self.edit)["outcome"], "rejected")

    def test_repository_instructions_require_the_approved_file_and_hash(self):
        path = self.checkout / "AGENTS.md"
        content = b"Keep the importer compatible.\n"
        task = {"instructions_sha256": {"AGENTS.md": digest(content)}}
        path.write_bytes(content)
        self.assertEqual(pi_run.repository_instructions(task, self.checkout),
                         [{"file": "AGENTS.md", "sha256": digest(content), "text": content.decode()}])
        for invalid in (b"Changed instructions", b"x" * (pi_run.LIMIT + 1), b""):
            path.write_bytes(invalid)
            with self.assertRaises(Stop):
                pi_run.repository_instructions(task, self.checkout)
        path.unlink()
        path.symlink_to(HERE / "task.json")
        with self.assertRaises(Stop):
            pi_run.repository_instructions(task, self.checkout)
        with self.assertRaises(Stop):
            pi_run.repository_instructions({}, self.checkout)

    def test_missing_approved_instructions_prevents_a_live_pi_process(self):
        with patch("pi_run.subprocess.Popen") as process, self.assertRaises(Stop) as error:
            pi_loop(self.run, self.tools, self.root, None, {"provider": "openai"})
        process.assert_not_called()
        self.assertEqual(error.exception.outcome, "invalid_input")

    def test_hash_edit_replay_conflict_and_single_attempt(self):
        self.assertEqual(self.tools.dispatch("harness_edit", {**self.edit, "expected_sha256": "stale"})["outcome"], "rejected")
        self.assertEqual(self.tools.dispatch("harness_edit", self.edit)["outcome"], "ok")
        self.assertTrue(self.tools.dispatch("harness_edit", self.edit)["replayed"])
        for invalid in ({**self.edit, "new": "three"}, {**self.edit, "operation_id": "two"},
                        {**self.edit, "file": "go/import_test.go"}):
            self.assertEqual(self.tools.dispatch("harness_edit", invalid)["outcome"], "rejected")
        self.assertEqual((self.checkout / "go/import.go").read_text(), "two\n")
        self.assertIn("+two", (self.run.path / "candidate.patch").read_text())

    def test_unknown_tools_and_arbitrary_checks_are_rejected_without_execution(self):
        with patch.object(self.run, "command") as command:
            for name, args in (("bash", {"command": "echo unsafe"}),
                               ("harness_check", {"preset": "go test ./..."}),
                               ("harness_read", {"file": "go/import.go", "extra": True})):
                self.assertEqual(self.tools.dispatch(name, args)["outcome"], "rejected")
            command.assert_not_called()

    def test_read_search_and_call_deadline_budgets(self):
        (self.checkout / "go/import.go").write_text("one\n" * 6000)
        result = self.tools.dispatch("harness_read", {"file": "go/import.go"})
        self.assertTrue(result["truncated"])
        self.assertEqual(len(result["text"]), 16384)
        result = self.tools.dispatch("harness_search", {"file": "go/import.go", "text": "one"})
        self.assertEqual(len(result["matches"]), 20)
        self.assertTrue(result["truncated"])
        self.tools.calls = MAX_CALLS
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_read", {"file": "go/import.go"})
        self.assertEqual(error.exception.outcome, "budget_exhausted")
        self.tools.calls = 0
        self.run.deadline = time.monotonic() - 1
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_read", {"file": "go/import.go"})
        self.assertEqual(error.exception.outcome, "timeout")

    def test_protected_test_change_prevents_check(self):
        (self.checkout / "go/import_test.go").write_text("tampered")
        with patch.object(self.run, "command") as command, self.assertRaises(Stop):
            self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        command.assert_not_called()

    def fake_node(self):
        binary = self.root / "node"
        binary.write_text(f"#!{sys.executable}\nimport os, time\nfrom pathlib import Path\n"
                          f"Path({str(self.root / 'pid')!r}).write_text(str(os.getpid()))\ntime.sleep(30)\n")
        binary.chmod(0o755)
        return patch.dict(os.environ, {"PATH": str(self.root)})

    def assert_child_removed(self):
        pid = int((self.root / "pid").read_text())
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)

    def test_timeout_removes_waiting_pi_process(self):
        self.run.deadline = time.monotonic() + 0.3
        with self.fake_node(), self.assertRaises(Stop) as error:
            pi_loop(self.run, self.tools, self.root, "good")
        self.assertEqual(error.exception.outcome, "timeout")
        self.assert_child_removed()

    def test_cancellation_during_pi_launch_cleans_up_child(self):
        popen = subprocess.Popen

        def cancel_after_launch(*args, **kwargs):
            process = popen(*args, **kwargs)
            until = time.monotonic() + 2
            while not (self.root / "pid").exists() and time.monotonic() < until:
                time.sleep(0.01)
            self.run.cancel_requested = True
            return process

        with self.fake_node(), patch("pi_run.subprocess.Popen", side_effect=cancel_after_launch), self.assertRaises(KeyboardInterrupt):
            pi_loop(self.run, self.tools, self.root, "good")
        self.assert_child_removed()

    def test_failed_preflight_does_not_start_pi(self):
        with patch.object(pi_run, "ContainerRun", return_value=self.run), \
                patch.object(pi_run, "preflight", side_effect=Stop("blocked", "No sandbox")), \
                patch.object(pi_run, "pi_loop") as loop, \
                patch.object(sys, "argv", ["pi_run.py", "--image", "sha256:" + "a" * 64, "--demo", "good"]):
            self.assertEqual(pi_run.main(), 1)
        loop.assert_not_called()
        self.assertFalse(self.run.result["startup_verified"])

    def test_live_failure_after_passing_check_stays_stopped(self):
        config = {"provider": "openai", "model": "chosen-model", "auth": "subscription",
                  "maxModelRequests": 2, "maxOutputTokensPerRequest": 1024}

        def fail_after_check(*_args):
            self.run.result["outcome"] = "passed"
            raise Stop("budget_exhausted", "No final model request allowed")

        with patch.object(pi_run, "ContainerRun", return_value=self.run), \
                patch.object(pi_run, "load_live_config", return_value=config), \
                patch.object(pi_run, "preflight"), patch.object(self.run, "prepare", create=True), \
                patch.object(pi_run, "prepare_checkout"), patch.object(pi_run, "Tools", return_value=self.tools), \
                patch.object(pi_run, "pi_loop", side_effect=fail_after_check), \
                patch.object(sys, "argv", ["pi_run.py", "--image", "sha256:" + "a" * 64, "--live"]):
            self.assertEqual(pi_run.main(), 1)
        self.assertEqual(self.run.result["outcome"], "budget_exhausted")
        self.assertIsNone(self.run.result["model_cost_usd"])

    def test_invalid_live_config_does_not_start_sandbox_or_pi(self):
        for failure in (Stop("invalid_input", "Bad limits"), FileNotFoundError("Missing config"),
                        json.JSONDecodeError("Malformed config", "{", 0)):
            with self.subTest(failure=type(failure).__name__), \
                    patch.object(pi_run, "ContainerRun", return_value=self.run), \
                    patch.object(pi_run, "load_live_config", side_effect=failure), \
                    patch.object(pi_run, "preflight") as preflight, patch.object(pi_run, "pi_loop") as loop, \
                    patch.object(sys, "argv", ["pi_run.py", "--image", "sha256:" + "a" * 64, "--live"]):
                self.assertEqual(pi_run.main(), 1)
            preflight.assert_not_called()
            loop.assert_not_called()
            receipt = json.loads((self.run.path / "result.json").read_text())
            self.assertEqual(receipt["workflow"], "pi-live-tools")
            self.assertEqual(receipt["candidate"], "model")
            self.assertIsNone(receipt["model"])
            self.assertEqual(receipt["model_requests"], 0)
            page = render_run(self.run.path / "result.json", self.run.path.parent.parent, receipt)
            self.assertIn("Pi: subscription request blocked", page)
            self.assertNotIn("prepared ineffective repair", page)

    def test_live_configuration_only_allows_openai_models(self):
        config = {"provider": "openai", "model": "locally-chosen", "auth": "subscription",
                  "maxModelRequests": 10, "maxOutputTokensPerRequest": None}
        with patch.object(Path, "read_text", return_value=json.dumps(config)):
            self.assertEqual(pi_run.load_live_config(), config)
        config["provider"] = "anthropic"
        with patch.object(Path, "read_text", return_value=json.dumps(config)), self.assertRaises(Stop) as error:
            pi_run.load_live_config()
        self.assertEqual(error.exception.outcome, "invalid_input")

    def test_token_summary_counts_cache_and_reasoning_as_subsets_and_reports_missing_usage(self):
        rows = [{"request": 1, "usage": {"inputTokens": 100, "cachedInputTokens": 20,
                 "outputTokens": 5000, "reasoningTokens": 4000, "totalTokens": 5100}},
                {"request": 2, "usage": {"inputTokens": 200, "cachedInputTokens": 40,
                 "outputTokens": 30, "reasoningTokens": 10, "totalTokens": 230}}]
        result = pi_run.token_summary(rows + rows[:1], 3)
        self.assertEqual(result["totals"], {"inputTokens": 300, "cachedInputTokens": 60,
                         "outputTokens": 5030, "reasoningTokens": 4010, "totalTokens": 5330})
        self.assertEqual(result["requests_with_usage"], 2)
        self.assertEqual(result["requests_without_usage"], 1)
        self.assertFalse(result["complete"])
        self.assertEqual(result["largest_response_output_tokens"], 5000)
        self.assertEqual(result["responses_over_4096_output_tokens"], 1)
        self.assertTrue(pi_run.token_summary(rows, 2)["complete"])


if __name__ == "__main__":
    unittest.main()
