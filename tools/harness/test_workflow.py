import copy
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

from native_sandbox import NativeRun
import operator_log
from run import Stop, digest
import workflow


class WorkflowTests(unittest.TestCase):
    def test_report_shows_recorded_attention_and_leaves_missing_time_unknown(self):
        result = {"outcome": "cancelled", "model_requests": 0, "candidate_source": "supplied", "mode": "plain",
                  "duration_seconds": 1, "operator": {"check_ins": 2, "corrections_submitted": 1, "attention_seconds": 12}}
        report = workflow.render(result)
        self.assertIn("check-ins: 2", report)
        self.assertIn("12 seconds", report)
        result["operator"]["attention_seconds"] = None
        self.assertIn("monitoring/review time: unknown", workflow.render(result))

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.run = NativeRun(self.root / "runs", "sha256:" + "a" * 64)
        self.run.result.update(model_requests=1, dispatches=[], provider_usage=[])
        operator_log.create(self.run.path)
        self.plan = {"mode": "managed", "checkpoint_limit": 4, "config": {"maxModelRequests": 10},
                     "budgets": {"tool_calls": 12}}
        self.sandbox = Mock(seeded="seed")
        self.tools = workflow.WorkflowTools(self.run, self.sandbox, self.plan, self.root)
        self.tools.reopen = Mock()
        self.tools.grade = Mock(side_effect=self.failed)
        self.message = {"agentOutcome": "finished", "messages": []}

    def failed(self):
        self.run.result.update(outcome="repair_failed", independent_checks_passed=False)
        self.run.result["verification_attempts"].append({"outcome": "repair_failed"})
        return False

    def test_managed_feedback_is_bounded_to_one_retry_without_private_examples(self):
        first = self.tools.checkpoint(self.message)
        self.assertEqual(first["action"], "continue")
        self.assertNotIn("TestHarnessJourneyDistanceVerifier", first["prompt"])
        self.assertEqual(self.tools.checkpoint(self.message)["action"], "stop")
        self.assertEqual(len(self.run.result["verification_attempts"]), 2)
        self.tools.reopen.assert_called_once()
        self.assertEqual(self.run.result["model_requests"], 1)

    def test_plain_mode_does_not_return_runner_feedback(self):
        self.plan["mode"] = "plain"
        self.assertEqual(self.tools.checkpoint(self.message), {"action": "stop"})
        self.tools.reopen.assert_not_called()
        self.assertEqual(self.run.result["continuations"], [])

    def test_request_tool_and_time_limits_prevent_an_extra_prompt(self):
        for limit in ("requests", "tools", "time"):
            with self.subTest(limit=limit):
                self.run.result["model_requests"], self.tools.calls, self.run.deadline = 1, 0, time.monotonic() + 10
                if limit == "requests":
                    self.run.result["model_requests"] = 10
                elif limit == "tools":
                    self.tools.calls = 12
                else:
                    self.run.deadline = time.monotonic() - 1
                self.assertEqual(self.tools.checkpoint(self.message), {"action": "stop"})
        self.tools.reopen.assert_not_called()

    def test_operator_correction_has_a_saved_identity_and_is_not_delivered_twice(self):
        event = operator_log.append(self.run.path, "feedback", "Inspect the expected behaviour", actor="supplied-test")
        self.assertEqual(self.tools.checkpoint(self.message)["action"], "continue")
        self.tools.grade.assert_not_called()
        self.assertEqual(self.run.result["continuations"][0]["operator_event"], event["id"])
        self.plan["mode"] = "plain"
        self.assertEqual(self.tools.checkpoint(self.message)["action"], "stop")
        self.assertEqual(len(self.run.result["continuations"]), 1)

    def test_a_transport_failure_cannot_be_promoted_to_workflow_success(self):
        def passed():
            self.run.result.update(outcome="native_verification_passed", independent_checks_passed=True)
            return True
        self.tools.grade.side_effect = passed
        self.assertEqual(self.tools.checkpoint({**self.message, "agentOutcome": "blocked"}), {"action": "stop"})
        self.assertFalse(self.run.result["independent_checks_passed"])
        self.assertEqual(self.run.result["outcome"], "blocked")

    def test_exhausted_tools_can_check_a_saved_patch_without_more_inference(self):
        self.tools.calls = 12
        def passed():
            self.run.result.update(outcome="native_verification_passed", independent_checks_passed=True)
            self.run.result["verification_attempts"].append({"outcome": "repository_verification_passed"})
            return True
        self.tools.grade.side_effect = passed
        self.assertEqual(self.tools.checkpoint({"agentOutcome": "budget_exhausted", "limit": "tool_calls"}), {"action": "stop"})
        self.assertTrue(self.run.result["independent_checks_passed"])
        self.assertEqual(self.run.result["agent_outcome"], "budget_exhausted")
        self.assertEqual(self.run.result["model_requests"], 1)
        self.tools.reopen.assert_not_called()

    def test_stop_recorded_during_checker_finalization_prevents_a_retry(self):
        def stopped():
            with patch("operator_log.os.kill"):
                operator_log.append(self.run.path, "stop", actor="supplied-test")
            return self.failed()
        self.tools.grade.side_effect = stopped
        with self.assertRaises(Stop) as caught:
            self.tools.checkpoint(self.message)
        self.assertEqual(caught.exception.outcome, "cancelled")
        self.tools.reopen.assert_not_called()
        self.assertEqual(self.run.result["continuations"], [])

    def test_missing_source_and_empty_patch_do_not_start_a_checker(self):
        self.tools.grade = workflow.WorkflowTools.grade.__get__(self.tools)
        self.run.result["snapshot_scope_valid"] = False
        with patch("workflow.check_candidate") as check, self.assertRaises(Stop):
            self.tools.grade()
        check.assert_not_called()
        self.run.result["snapshot_scope_valid"] = True
        (self.run.path / "candidate.patch").write_bytes(b"")
        with patch("workflow.check_candidate") as check:
            self.assertFalse(self.tools.grade())
        check.assert_not_called()

    def test_spent_plan_stops_before_model_or_container_work(self):
        directory = self.root / "plan"
        directory.mkdir()
        workflow.baseline.claim(directory, "spent")
        plan = {"image": self.run.image, "config": {"model": "test", "provider": "openai"}, "mode": "plain", "budgets": {}}
        with patch("workflow.load_plan", return_value=plan), patch("workflow.preflight") as preflight, \
                patch("workflow.REPORTS", self.root), patch("workflow.session") as session:
            _, result = workflow.execute(directory)
        self.assertEqual(result["outcome"], "budget_exhausted")
        self.assertEqual(result["model_requests"], 0)
        self.assertEqual(result["containers"], [])
        preflight.assert_not_called()
        session.assert_not_called()

    def test_cleanup_crossing_the_deadline_changes_the_saved_success_to_timeout(self):
        agent = self.root / "suite/cases/task/agent"
        agent.mkdir(parents=True)
        (agent / "source").write_text("fixture")
        plan = {"image": self.run.image, "mode": "plain", "checkpoint_limit": 4,
                "config": {"model": "fixture", "provider": "openai", "maxModelRequests": 10},
                "budgets": {"tool_calls": 30, "deadline_seconds": 600}, "case": {"id": "task"},
                "suite": str(self.root / "suite"), "context": {"task": {}, "prompt": "fixture"},
                "evaluator": {}, "runtime": {}, "environment": {}, "seccomp_sha256": "profile"}
        for finish in (699.95, 700.1):
            with self.subTest(finish=finish):
                run = NativeRun(self.root / str(finish), self.run.image)
                run.started, run.deadline = 100, 700
                directory = self.root / ("plan-" + str(finish))
                directory.mkdir()
                clock = {"now": 699.9}
                sandbox = Mock(seeded="fixture")
                sandbox.close.side_effect = lambda: clock.update(now=finish)
                def checked(target, tools, *args):
                    tools.checkpoints = 1
                    target.result.update(outcome="native_verification_passed", independent_checks_passed=True, first_repair_passed=True)
                with patch("workflow.load_plan", return_value=plan), patch("workflow.NativeRun", return_value=run), \
                        patch("workflow.prepare_verification", side_effect=lambda target, case: target.result.update(evaluator={})), \
                        patch("workflow.preflight", side_effect=lambda target: target.result.update(runtime={}, environment={}, seccomp_sha256="profile")), \
                        patch.object(run, "prepare"), patch("workflow.NativeSandbox", return_value=sandbox), \
                        patch("workflow.session", side_effect=checked), patch("workflow.time.monotonic", side_effect=lambda: clock["now"]):
                    path, result = workflow.execute(directory)
                expected = "timeout" if finish > 700 else "native_verification_passed"
                self.assertEqual(result["outcome"], expected)
                self.assertEqual(result["independent_checks_passed"], finish <= 700)
                self.assertEqual(json.loads((path / "result.json").read_text())["outcome"], expected)


class WorkflowReceiptTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.directory = self.root / "workflows/runs" / ("a" * 32)
        self.directory.mkdir(parents=True)
        operator_log.create(self.directory)
        self.plan = {"mode": "managed", "config": {"maxModelRequests": 10}, "image": "image",
                     "budgets": {"tool_calls": 12, "deadline_seconds": 600},
                     "evaluator": {"task_sha256": "task"}, "case": {"id": "task"}}
        self.enterContext(patch("workflow.load_plan", return_value=self.plan))
        self.complete_match = self.enterContext(patch("workflow.receipt_matches", return_value=True))
        self.result = {"run_id": self.directory.name, "plan": "saved-plan", "plan_sha256": workflow.fingerprint(self.plan),
                       "mode": "managed", "config": self.plan["config"], "image": "image", "budgets": self.plan["budgets"],
                       "model_requests": 0, "tool_calls": 2, "dispatches": [], "candidate_source": "supplied",
                       "task_sha256": "task", "evaluator_sha256": "rules", "independent_checks_passed": True,
                       "outcome": "native_verification_passed", "duration_seconds": 2, "agent_outcome": "finished",
                       "containers": [{"removed": True}], "snapshot_scope_valid": True,
                       "first_repair_passed": False, "continuations": [{"origin": "runner"}], "verification_attempts": []}
        self.children = []
        for index, outcome in enumerate(("repair_failed", "repository_verification_passed"), 1):
            identity = str(index) * 32
            child = self.root / "verification/runs" / identity
            child.mkdir(parents=True)
            attempt = self.directory / "attempts" / str(index)
            attempt.mkdir(parents=True)
            raw_patch = f"patch-{index}".encode()
            (attempt / "candidate.patch").write_bytes(raw_patch)
            checked = {"parent_run_id": self.directory.name, "attempt": index, "image": "image", "task_sha256": "task",
                       "evaluator_sha256": "rules", "evaluator": self.plan["evaluator"], "task": "task", "outcome": outcome,
                       "patch_sha256": digest(raw_patch), "checks": [], "containers": [{"removed": True}]}
            raw = json.dumps(checked).encode()
            (child / "result.json").write_bytes(raw)
            self.children.append(child)
            self.result["verification_attempts"].append({"run_id": identity, "receipt_sha256": digest(raw), "outcome": outcome,
                                                         "patch_sha256": digest(raw_patch), "checks": [],
                                                         "containers_created": 1, "containers_removed": 1})
        (self.directory / "candidate.patch").write_bytes(b"patch-2")
        self.save()

    def save(self):
        (self.directory / "result.json").write_text(json.dumps(self.result))

    def test_each_reported_attempt_links_to_its_own_frozen_patch(self):
        report = workflow.render(self.result)
        for index, attempt in enumerate(self.result["verification_attempts"], 1):
            with self.subTest(attempt=index):
                section = report.split(f"Attempt {index}:", 1)[1].split("\nAttempt ", 1)[0]
                link = section.split("[saved candidate patch](", 1)[1].split(")", 1)[0]
                patch_bytes = (self.directory / link).read_bytes()
                self.assertEqual(digest(patch_bytes), attempt["patch_sha256"])

    def test_changed_parent_outcome_or_first_repair_flag_cannot_relabel_a_failed_attempt(self):
        self.assertTrue(workflow.validated_result(self.directory, self.root)["independent_checks_passed"])
        original = copy.deepcopy(self.result)
        self.result["verification_attempts"][0]["outcome"] = "repository_verification_passed"
        self.save()
        with self.assertRaises(Stop):
            workflow.validated_result(self.directory, self.root)
        self.result = original
        self.result["first_repair_passed"] = True
        self.save()
        with self.assertRaises(Stop):
            workflow.validated_result(self.directory, self.root)

    def test_cancelled_child_remains_readable_but_can_never_establish_a_pass(self):
        child = self.children[-1]
        checked = json.loads((child / "result.json").read_text())
        checked["outcome"] = "cancelled"
        (child / "candidate.stdout").write_bytes(b"partial output")
        (child / "candidate.stderr").write_bytes(b"")
        checked["checks"] = [{"id": "candidate", "outcome": "cancelled", "exit_code": -9,
                              "stdout": "candidate.stdout", "stderr": "candidate.stderr",
                              "stdout_sha256": digest(b"partial output"), "stderr_sha256": digest(b"")}]
        raw = json.dumps(checked).encode()
        (child / "result.json").write_bytes(raw)
        record = self.result["verification_attempts"][-1]
        record.update(outcome="cancelled", receipt_sha256=digest(raw), checks=[{"id": "candidate", "outcome": "cancelled", "exit_code": -9}])
        self.result.update(outcome="cancelled", independent_checks_passed=False)
        self.save()
        parsed = workflow.validated_result(self.directory, self.root)
        self.assertTrue(parsed["partial_verification_evidence"])
        self.assertFalse(parsed["independent_checks_passed"])
        self.result.update(outcome="native_verification_passed", independent_checks_passed=True)
        self.save()
        with self.assertRaises(Stop):
            workflow.validated_result(self.directory, self.root)
        self.result.update(outcome="cancelled", independent_checks_passed=False)
        self.save()
        (child / "candidate.stdout").write_bytes(b"changed")
        with self.assertRaises(Stop):
            workflow.validated_result(self.directory, self.root)


if __name__ == "__main__":
    unittest.main()
