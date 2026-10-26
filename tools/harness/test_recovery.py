import copy
import json
import os
import time
import sys
import subprocess
import unittest
from unittest.mock import patch

from recover import archive_pi, execute, finalize, main, restore_run
from recovery import Journal, RecoveryTools, durable_json, lock_session, request_cancel
from pi_run import MAX_CALLS, ModelRequestLimit
from repair import repair_receipt_matches
from run import Stop
from test_repair import RepairFixture


class RecoveryTests(RepairFixture, unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.enterContext(patch("recovery.REPORTS", self.root))
        self.enterContext(patch("recover.REPORTS", self.root))
        self.journal = Journal(self.run, self.checkout)
        self.journal.state["demo"] = "retry"
        self.journal.save()
        self.tools = RecoveryTools(self.run, self.journal)

    def resume(self):
        self.run = restore_run(self.run.path)
        self.run.prepared = True
        self.journal = Journal(self.run)
        self.journal.reconcile()
        self.tools = RecoveryTools(self.run, self.journal)
        self.tools.recover_pending()

    def interrupt(self, fault, count=2):
        self.journal.fault = fault
        calls = self.tools.prepared_calls("retry")
        with self.assertRaises(Stop) as error:
            for call in calls[:count]:
                self.tools.dispatch(call["name"], call["args"])
        self.assertEqual(error.exception.outcome, "incomplete")
        self.assertIn(fault, error.exception.reason)
        self.journal.fault = None

    def finish_calls(self):
        for call in self.tools.prepared_calls("retry"):
            self.tools.dispatch(call["name"], call["args"])
        self.tools.finish()

    def crash_at(self, location):
        call = self.tools.prepared_calls("retry")[1]
        pid = os.fork()
        if pid == 0:
            def crash(checkpoint):
                if checkpoint == location:
                    self.journal.record("process_exit_injected", location=location, exit_code=73)
                    os._exit(73)
            self.journal.checkpoint = crash
            self.tools.dispatch("harness_edit", call["args"])
            os._exit(74)
        _, status = os.waitpid(pid, 0)
        self.assertEqual(os.waitstatus_to_exitcode(status), 73)
        self.resume()
        self.assertEqual(self.journal.state["requests"][-1]["result"]["outcome"], "ok")
        self.assertEqual(self.tools.edits, 1)

    def test_process_exit_before_write_retains_intent_without_finally(self):
        self.crash_at("before-edit")

    def test_process_exit_after_write_retains_intent_without_finally(self):
        self.crash_at("after-write")

    def test_late_cancellation_is_serialized_with_publication(self):
        self.finish_calls()
        record = self.journal.record
        children = []

        def cancel_while_publishing(kind, **fields):
            if kind == "session_stopped":
                code = "import sys; from pathlib import Path; from recovery import request_cancel; sys.exit(1 if request_cancel(Path(sys.argv[1])) else 0)"
                children.append(subprocess.Popen([sys.executable, "-c", code, str(self.run.path)],
                                                 env={**os.environ, "PYTHONPATH": "tools/harness"}))
            return record(kind, **fields)

        with patch.object(self.journal, "record", side_effect=cancel_while_publishing):
            finalize(self.run, self.journal)
        self.assertEqual(children[0].wait(timeout=5), 0)
        self.assertFalse((self.run.path / "cancel.json").exists())
        self.assertEqual(json.loads((self.run.path / "result.json").read_text())["outcome"], "verified_repair_passed")

    def test_cancellation_before_publication_prevents_success(self):
        self.finish_calls()
        request_cancel(self.run.path)
        finalize(self.run, self.journal)
        self.assertEqual(self.run.result["outcome"], "cancelled")
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_changed_disk_before_publication_prevents_success(self):
        self.finish_calls()
        (self.checkout / "go/import.go").write_text("Human change\n")
        finalize(self.run, self.journal)
        self.assertEqual(self.run.result["outcome"], "conflict")
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_malformed_journal_preserves_evidence_and_records_incomplete(self):
        self.finish_calls()
        finalize(self.run, self.journal)
        state = json.loads((self.run.path / "journal.json").read_text())
        state["files"] = None
        durable_json(self.run.path / "journal.json", state)
        receipt = (self.run.path / "result.json").read_bytes()
        (self.root / "recovery").symlink_to(self.root / "repair", target_is_directory=True)
        with patch("recover.REPORTS", self.root), patch.object(sys, "argv", ["recover.py", "--resume", self.run.path.name]):
            self.assertEqual(main(), 1)
        self.assertEqual((self.run.path / "result.json").read_bytes(), receipt)
        self.assertEqual(json.loads((self.run.path / "recovery-stop.json").read_text())["outcome"], "incomplete")

    def test_malformed_nested_entries_cannot_overwrite_the_original_receipt(self):
        self.finish_calls()
        finalize(self.run, self.journal)
        state = json.loads((self.run.path / "journal.json").read_text())
        receipt = (self.run.path / "result.json").read_bytes()
        (self.root / "recovery").symlink_to(self.root / "repair", target_is_directory=True)
        mutations = (lambda s: s["operations"].update({"repair-1": None}),
                     lambda s: s["operations"]["repair-1"].update(args=None),
                     lambda s: s["requests"].append(None),
                     lambda s: s["result"]["attempts"].append(None))
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                malformed = copy.deepcopy(state)
                mutate(malformed)
                durable_json(self.run.path / "journal.json", malformed)
                if malformed["result"] != state["result"]:
                    durable_json(self.run.path / "result.json", malformed["result"])
                saved = (self.run.path / "result.json").read_bytes()
                with patch.object(sys, "argv", ["recover.py", "--resume", self.run.path.name]):
                    self.assertEqual(main(), 1)
                self.assertEqual((self.run.path / "result.json").read_bytes(), saved)
                self.assertEqual(json.loads((self.run.path / "recovery-stop.json").read_text())["outcome"], "incomplete")
                (self.run.path / "result.json").write_bytes(receipt)

    def test_terminal_handoffs_request_inspection_instead_of_resume(self):
        for status in ("budget_exhausted", "invalid_input", "cancelled", "timeout", "conflict", "blocked"):
            self.journal.state["status"] = status
            self.assertEqual(self.journal.handoff()["next_permitted_action"], "inspect; start a new bounded task")

    def test_receipt_newer_than_journal_is_preserved_for_reconstruction(self):
        newer = {**self.run.result, "newer_observation": "verification started"}
        durable_json(self.run.path / "result.json", newer)
        with self.assertRaises(Stop) as error:
            restore_run(self.run.path)
        self.assertEqual(error.exception.outcome, "incomplete")
        self.assertEqual(json.loads((self.run.path / "result.json").read_text()), newer)

    def test_each_pi_entry_preserves_its_context_conversation_and_stderr(self):
        for name in ("context.json", "pi-session.json", "pi.stderr"):
            (self.run.path / name).write_text("First session")
        archive_pi(self.run.path, 1)
        for name in ("context.json", "pi-session.json", "pi.stderr"):
            (self.run.path / name).write_text("Second session")
        archive_pi(self.run.path, 2)
        for name in ("context.json", "pi-session.json", "pi.stderr"):
            self.assertEqual((self.run.path / "pi-sessions/1" / name).read_text(), "First session")
            self.assertEqual((self.run.path / "pi-sessions/2" / name).read_text(), "Second session")

    def enable_live(self, limit=2):
        config = {"provider": "openai", "model": "test-model", "auth": "subscription",
                  "maxModelRequests": limit, "maxOutputTokensPerRequest": None}
        self.run.result.update(live_config=config, provider="openai", model="test-model", auth="subscription",
                               model_requests=0, model_requests_dispatched=0, provider_usage=[])
        self.run.result["budgets"].update(model_requests=limit, output_tokens_per_request=None)
        self.journal.state.update(live_config=config, model_reservations=[])
        self.journal.save()
        self.tools.refresh_context()
        return config

    def model_message(self, request):
        return {"type": "model_reserve", "id": f"model-{request}", "request": request,
                "provider": "openai", "model": "test-model"}

    def exhaust_model(self, count=2):
        self.enable_live(limit=1)
        for call in self.tools.prepared_calls("retry")[:count]:
            self.tools.dispatch(call["name"], call["args"])
        self.tools.reserve_model(self.model_message(1))

    def test_final_edit_is_verified_at_the_model_limit_without_another_pi_entry(self):
        self.exhaust_model(count=5)
        with patch("recover.preflight"), patch("recover.pi_loop") as loop:
            execute(self.run, self.journal, "retry")
        loop.assert_not_called()
        self.assertEqual(self.run.result["outcome"], "verified_repair_passed")
        self.assertEqual(len(self.journal.state["requests"]), 6)
        self.assertEqual(len(self.journal.state["model_reservations"]), 1)
        self.assertTrue(repair_receipt_matches(self.run.result, self.run.path, self.root))
        finalize(self.run, self.journal)
        self.resume()
        with patch("recover.pi_loop") as loop:
            execute(self.run, self.journal, "retry")
        loop.assert_not_called()
        self.assertEqual(len(self.journal.state["requests"]), 6)

    def test_comparison_control_stops_at_model_limit_without_automatic_checks(self):
        self.exhaust_model(count=5)
        self.run.result["automatic_final_check"] = False
        self.journal.save()
        before = len(self.run.result["attempts"])
        with patch("recover.preflight"), patch("recover.pi_loop") as loop, self.assertRaises(ModelRequestLimit):
            execute(self.run, self.journal, "retry")
        loop.assert_not_called()
        self.assertEqual(len(self.run.result["attempts"]), before)
        self.assertEqual(len(self.journal.state["requests"]), 5)
        self.assertNotEqual(self.run.result["outcome"], "verified_repair_passed")

    def test_final_verification_after_a_live_limit_uses_the_normal_journaled_tool(self):
        self.enable_live(limit=1)

        def stop_at_limit(run, tools, *args):
            for call in tools.prepared_calls("retry")[:5]:
                tools.dispatch(call["name"], call["args"])
            tools.reserve_model(self.model_message(1))
            raise ModelRequestLimit("Expected model-request limit")

        with patch("recover.preflight"), patch("recover.pi_loop", side_effect=stop_at_limit):
            execute(self.run, self.journal, "retry")
        self.assertEqual(self.run.result["outcome"], "verified_repair_passed")
        record = next(r for r in self.journal.state["records"] if r["kind"] == "final_verification_requested")
        self.assertEqual(record["origin"], "runner")
        self.assertEqual(record["request"], 6)
        self.assertEqual(self.journal.state["requests"][-1]["name"], "harness_check")

    def test_failed_final_verification_keeps_the_model_limit_and_no_success(self):
        self.exhaust_model()
        with patch("recover.preflight"), self.assertRaises(ModelRequestLimit):
            execute(self.run, self.journal, "retry")
        self.assertEqual(self.run.result["attempts"][0]["outcome"], "repair_failed")
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))
        self.assertEqual(len(self.journal.state["requests"]), 3)

    def test_final_verification_cannot_exceed_the_tool_limit(self):
        self.exhaust_model()
        while self.tools.calls < MAX_CALLS:
            self.tools.dispatch("harness_read", {"file": "go/import.go"})
        with patch("recover.preflight"), self.assertRaises(Stop) as error:
            execute(self.run, self.journal, "retry")
        self.assertEqual(error.exception.outcome, "budget_exhausted")
        self.assertEqual(len(self.journal.state["requests"]), MAX_CALLS)
        self.assertEqual(self.run.result["attempts"], [])

    def test_model_limit_without_an_edit_does_not_start_verification(self):
        self.exhaust_model(count=0)
        with patch("recover.preflight"), self.assertRaises(ModelRequestLimit):
            execute(self.run, self.journal, "retry")
        self.assertEqual(self.journal.state["requests"], [])
        self.assertEqual(self.run.result["attempts"], [])

    def test_unrelated_model_failure_at_the_limit_cannot_start_verification(self):
        self.enable_live(limit=1)

        def fail(run, tools, *args):
            for call in tools.prepared_calls("retry")[:2]:
                tools.dispatch(call["name"], call["args"])
            tools.reserve_model(self.model_message(1))
            raise Stop("budget_exhausted", "Transport retry refused")

        with patch("recover.preflight"), patch("recover.pi_loop", side_effect=fail), self.assertRaises(Stop):
            execute(self.run, self.journal, "retry")
        self.assertEqual(self.run.result["attempts"], [])

    def test_final_verification_honours_cancellation_deadline_and_conflicts(self):
        self.enable_live(limit=1)
        for call in self.tools.prepared_calls("retry")[:2]:
            self.tools.dispatch(call["name"], call["args"])
        for outcome in ("cancelled", "timeout", "conflict"):
            with self.subTest(outcome=outcome):
                def stop(run, tools, *args):
                    tools.reserve_model(self.model_message(1))
                    if outcome == "cancelled":
                        request_cancel(run.path)
                    elif outcome == "timeout":
                        run.deadline = time.monotonic() - 1
                    else:
                        (self.checkout / "go/import.go").write_text("Human change\n")
                    raise ModelRequestLimit("Expected model-request limit")

                with patch("recover.preflight"), patch("recover.pi_loop", side_effect=stop), self.assertRaises(Stop) as error:
                    execute(self.run, self.journal, "retry")
                self.assertEqual(error.exception.outcome, outcome)
                self.assertEqual(self.run.result["attempts"], [])
                (self.run.path / "cancel.json").unlink(missing_ok=True)
                self.run.deadline = time.monotonic() + 600
                self.journal.state["model_reservations"] = []
                self.run.result["model_requests"] = 0

    def test_interrupted_final_verification_cannot_be_replayed_on_resume(self):
        self.exhaust_model(count=5)
        with patch("recover.preflight"), self.assertRaises(Stop) as error:
            execute(self.run, self.journal, "retry", fault="during-verification")
        self.assertEqual(error.exception.outcome, "incomplete")
        self.journal.fault = None
        with self.assertRaises(Stop) as error:
            self.resume()
        self.assertEqual(error.exception.outcome, "incomplete")

    def test_model_request_is_durable_before_approval_and_survives_a_lost_reply(self):
        self.enable_live()
        self.assertEqual(self.tools.reserve_model(self.model_message(1)), {"outcome": "ok", "request": 1})
        saved = json.loads((self.run.path / "journal.json").read_text())
        self.assertEqual(saved["result"]["model_requests"], 1)
        self.resume()
        self.assertEqual(self.tools.start_context["modelRequestsUsed"], 1)
        self.tools.reserve_model(self.model_message(2))
        with self.assertRaises(Stop) as error:
            self.tools.reserve_model(self.model_message(3))
        self.assertEqual(error.exception.outcome, "budget_exhausted")
        self.assertEqual(len(self.journal.state["model_reservations"]), 2)

    def test_model_reservations_cannot_change_identity_or_replay_an_uncertain_request(self):
        self.enable_live()
        self.tools.reserve_model(self.model_message(1))
        for message in (self.model_message(1), {**self.model_message(2), "model": "other-model"},
                        {**self.model_message(2), "provider": "other-provider"}):
            with self.assertRaises(Stop) as error:
                self.tools.reserve_model(message)
            self.assertEqual(error.exception.outcome, "invalid_input")
        self.assertEqual(len(self.journal.state["model_reservations"]), 1)

    def test_malformed_model_budget_or_changed_mode_requires_reconstruction(self):
        self.enable_live()
        self.tools.reserve_model(self.model_message(1))
        saved = copy.deepcopy(self.journal.state)
        mutations = (lambda s: s.update(model_reservations=None),
                     lambda s: s["result"].update(model_requests=0),
                     lambda s: s.update(live_config=None),
                     lambda s: s["model_reservations"].append(None))
        for mutate in mutations:
            state = copy.deepcopy(saved)
            mutate(state)
            durable_json(self.run.path / "journal.json", state)
            with self.assertRaises(Stop) as error:
                Journal(self.run)
            self.assertEqual(error.exception.outcome, "incomplete")

    def test_uncertain_dispatch_keeps_its_request_spent_and_usage_unknown(self):
        self.enable_live()
        self.tools.reserve_model(self.model_message(1))
        request_cancel(self.run.path)
        finalize(self.run, self.journal)
        summary = self.run.result["token_summary"]
        self.assertEqual(summary["requests_reserved"], 1)
        self.assertEqual(summary["observed_dispatches"], 0)
        self.assertEqual(summary["requests_without_usage"], 1)
        self.assertFalse(summary["complete"])

    def test_live_recovery_context_requires_checking_the_already_applied_edit(self):
        self.enable_live()
        self.tools.reserve_model(self.model_message(1))
        self.interrupt("after-write")
        self.resume()
        self.tools.refresh_context()
        context = self.tools.start_context
        self.assertEqual(context["modelRequestsUsed"], 1)
        self.assertEqual(context["recovery"]["applied_edits"], 1)
        self.assertEqual(context["recovery"]["next_required_tool"], "harness_check")
        self.assertTrue(context["recovery"]["last_tool_result"]["result"]["replayed"])

    def test_malformed_saved_usage_is_refused_without_overwriting_evidence(self):
        self.enable_live()
        self.tools.reserve_model(self.model_message(1))
        saved = copy.deepcopy(self.journal.state)
        (self.root / "recovery").symlink_to(self.root / "repair", target_is_directory=True)
        for malformed in (None, [None], [{"request": 1, "usage": None}], [{"request": 1, "usage": {}}]):
            state = copy.deepcopy(saved)
            state["result"]["provider_usage"] = malformed
            durable_json(self.run.path / "journal.json", state)
            durable_json(self.run.path / "result.json", state["result"])
            receipt = (self.run.path / "result.json").read_bytes()
            with patch.object(sys, "argv", ["recover.py", "--resume", self.run.path.name]):
                self.assertEqual(main(), 1)
            self.assertEqual((self.run.path / "result.json").read_bytes(), receipt)
            self.assertEqual(json.loads((self.run.path / "recovery-stop.json").read_text())["outcome"], "incomplete")

    def test_malformed_new_usage_cannot_crash_finalization_or_publish_success(self):
        self.enable_live()
        self.run.result["provider_usage"] = None
        finalize(self.run, self.journal)
        self.assertEqual(self.run.result["outcome"], "incomplete")
        self.assertFalse(self.run.result["token_summary"]["complete"])

    def read_edit(self, file="go/import.go"):
        args = self.tools.prepared_calls("retry")[1]["args"]
        observed = self.tools.dispatch("harness_read", {"file": file})
        return observed, {key: value for key, value in args.items() if key != "expected_sha256"} | {"read_id": observed["read_id"]}

    def test_read_reference_keeps_the_full_hash_and_replays_without_duplicate_edit(self):
        observed, args = self.read_edit()
        result = self.tools.dispatch("harness_edit", args)
        self.assertEqual(result["outcome"], "ok")
        intent = self.journal.state["operations"][args["operation_id"]]
        self.assertEqual(intent["args"]["expected_sha256"], observed["sha256"])
        self.assertEqual(self.journal.state["requests"][-1]["args"]["read_id"], observed["read_id"])
        self.assertTrue(self.tools.dispatch("harness_edit", args)["replayed"])
        self.assertEqual(self.tools.edits, 1)

    def test_unknown_wrong_file_and_ambiguous_read_references_cannot_write(self):
        observed, args = self.read_edit("go/import_test.go")
        before = (self.checkout / "go/import.go").read_bytes()
        invalid = (args, {**args, "read_id": "unknown"}, {**args, "read_id": []},
                   {**args, "expected_sha256": observed["sha256"]})
        for value in invalid:
            self.assertEqual(self.tools.dispatch("harness_edit", value)["outcome"], "rejected")
        self.assertEqual((self.checkout / "go/import.go").read_bytes(), before)
        self.assertFalse(self.journal.state["operations"])

    def test_old_read_reference_does_not_upgrade_to_the_current_file_hash(self):
        _observed, args = self.read_edit()
        self.tools.dispatch("harness_edit", args)
        failed = self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        self.assertEqual(failed["outcome"], "repair_failed")
        before = (self.checkout / "go/import.go").read_bytes()
        result = self.tools.dispatch("harness_edit", {**args, "operation_id": "second", "old": '"fit"', "new": '"FIT"'})
        self.assertEqual(result["outcome"], "rejected")
        self.assertIn("File changed", result["explanation"])
        self.assertEqual((self.checkout / "go/import.go").read_bytes(), before)

    def test_pending_read_reference_survives_lost_reply_and_fresh_context(self):
        observed, args = self.read_edit()
        self.journal.fault = "after-write"
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_edit", args)
        self.assertEqual(error.exception.outcome, "incomplete")
        self.journal.fault = None
        with patch.object(RecoveryTools, "write_edit", side_effect=AssertionError("duplicate write")):
            self.resume()
        self.assertIn(observed["read_id"], self.tools.reads)
        self.assertTrue(self.journal.state["requests"][-1]["result"]["replayed"])
        self.finish_calls()
        self.assertEqual(self.run.result["outcome"], "verified_repair_passed")

    def test_human_change_after_saved_read_is_preserved(self):
        _observed, args = self.read_edit()
        path = self.checkout / "go/import.go"
        path.write_text("Human change\n")
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_edit", args)
        self.assertEqual(error.exception.outcome, "conflict")
        self.assertEqual(path.read_text(), "Human change\n")

    def test_malformed_saved_read_id_or_hash_requires_reconstruction(self):
        self.read_edit()
        saved = copy.deepcopy(self.journal.state)
        for field, value in (("read_id", "other-session"), ("sha256", "bad-hash"), ("file", "go/other.go")):
            state = copy.deepcopy(saved)
            state["requests"][0]["result"][field] = value
            durable_json(self.run.path / "journal.json", state)
            with self.assertRaises(Stop) as error:
                Journal(self.run)
            self.assertEqual(error.exception.outcome, "incomplete")

    def test_intent_before_write_survives_a_fresh_runner(self):
        before = (self.checkout / "go/import.go").read_bytes()
        self.interrupt("before-edit")
        self.assertEqual((self.checkout / "go/import.go").read_bytes(), before)
        self.resume()
        self.finish_calls()
        self.assertTrue(repair_receipt_matches(self.run.result, self.run.path, self.root))
        decisions = [r["decision"] for r in self.journal.state["records"] if r["kind"] == "edit_reconciled"]
        self.assertIn("unchanged", decisions)
        self.assertEqual(len(self.journal.state["operations"]), 2)

    def test_lost_reply_accepts_observed_write_without_a_second_write(self):
        self.interrupt("after-write")
        after = (self.checkout / "go/import.go").read_bytes()
        with patch.object(RecoveryTools, "write_edit", side_effect=AssertionError("duplicate write")):
            self.resume()
        self.assertEqual((self.checkout / "go/import.go").read_bytes(), after)
        self.assertTrue(self.journal.state["requests"][-1]["result"]["replayed"])
        self.finish_calls()
        self.assertEqual(self.run.result["outcome"], "verified_repair_passed")
        self.assertEqual(len(self.journal.state["requests"]), 6)

    def test_conflicting_human_change_is_preserved_and_cannot_verify(self):
        self.interrupt("after-write")
        path = self.checkout / "go/import.go"
        path.write_text("Human change\n")
        with self.assertRaises(Stop) as error:
            self.resume()
        self.assertEqual(error.exception.outcome, "conflict")
        self.assertEqual(path.read_text(), "Human change\n")
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_a_completed_edit_replay_rechecks_disk_and_refuses_conflict(self):
        calls = self.tools.prepared_calls("retry")
        for call in calls[:2]:
            self.tools.dispatch(call["name"], call["args"])
        (self.checkout / "go/import.go").write_text("Human change\n")
        with self.assertRaises(Stop) as error:
            self.tools.dispatch(calls[1]["name"], calls[1]["args"])
        self.assertEqual(error.exception.outcome, "conflict")

    def test_stale_handoff_and_model_claim_cannot_skip_failed_verification(self):
        self.interrupt("after-write")
        durable_json(self.run.path / "handoff.json", {"next_permitted_action": "publish success", "last_observed_check": "passed"})
        self.run.result["model_claim"] = "Done; every check passed"
        self.journal.save()
        self.resume()
        self.assertNotEqual(self.tools.start_context["recovery"]["last_observed_check"], "passed")
        self.assertFalse(self.run.result["attempts"])
        self.finish_calls()
        self.assertEqual([a["outcome"] for a in self.run.result["attempts"]],
                         ["repair_failed", "repository_verification_passed"])

    def test_interruption_during_verification_stops_with_an_incomplete_receipt(self):
        for call in self.tools.prepared_calls("retry")[:5]:
            self.tools.dispatch(call["name"], call["args"])
        self.journal.fault = "during-verification"
        with self.assertRaises(Stop) as error:
            self.tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        self.assertEqual(error.exception.outcome, "incomplete")
        self.assertEqual(self.run.result["attempts"][-1]["outcome"], "incomplete")
        self.journal.fault = None
        with self.assertRaises(Stop) as error:
            self.resume()
        self.assertEqual(error.exception.outcome, "incomplete")
        self.assertFalse(repair_receipt_matches(self.run.result, self.run.path, self.root))

    def test_interruption_before_final_receipt_revalidates_existing_evidence(self):
        for call in self.tools.prepared_calls("retry"):
            self.tools.dispatch(call["name"], call["args"])
        self.journal.fault = "before-receipt"
        with self.assertRaises(Stop):
            self.tools.finish()
        self.journal.fault = None
        self.resume()
        with patch.object(RecoveryTools, "check_candidate", side_effect=AssertionError("duplicate check")):
            self.tools.finish()
        self.assertEqual(self.run.result["outcome"], "verified_repair_passed")
        self.assertEqual(len(self.run.result["attempts"]), 2)

    def test_changed_receipt_or_protected_file_prevents_resume(self):
        self.finish_calls()
        saved = copy.deepcopy(self.run.result)
        last = self.run.result["attempts"][-1]
        path = self.root / "verification/runs" / last["verification_run_id"] / "result.json"
        path.write_text("{}")
        with self.assertRaises(Stop) as error:
            self.journal.reconcile()
        self.assertEqual(error.exception.outcome, "incomplete")
        self.run.result = saved
        (self.checkout / "go/import_test.go").write_text("weakened")
        with self.assertRaises(Stop) as error:
            self.journal.reconcile()
        self.assertEqual(error.exception.outcome, "conflict")

    def test_missing_journal_or_source_requires_deliberate_reconstruction(self):
        for raw in ("{", "{}"):
            (self.run.path / "journal.json").write_text(raw)
            with self.assertRaises(Stop) as error:
                restore_run(self.run.path)
            self.assertEqual(error.exception.outcome, "incomplete")
        self.journal.save()
        self.journal.checkout = self.root / "missing"
        with self.assertRaises(Stop) as error:
            self.journal.reconcile()
        self.assertEqual(error.exception.outcome, "incomplete")

    def test_cancel_is_repeatable_and_prevents_a_late_success(self):
        self.finish_calls()
        request_cancel(self.run.path)
        request_cancel(self.run.path)
        with self.assertRaises(Stop) as error:
            self.tools.finish()
        self.assertEqual(error.exception.outcome, "cancelled")
        self.assertTrue((self.run.path / "candidate.patch").is_file())

    def test_active_cancel_targets_the_matching_process_only(self):
        durable_json(self.run.path / "owner.json", {"pid": os.getpid(), "identity": "stale"})
        with patch("recovery.os.kill") as kill:
            request_cancel(self.run.path)
        kill.assert_not_called()

    def test_resume_cannot_reset_time_tools_or_recovery_budgets(self):
        self.journal.state["expires_at"] = time.time() - 1
        self.journal.save()
        self.run = restore_run(self.run.path)
        self.journal = Journal(self.run)
        with self.assertRaises(Stop) as error:
            self.journal.checkpoint("resume")
        self.assertEqual(error.exception.outcome, "timeout")
        self.run.deadline = time.monotonic() + 10
        self.journal.state["requests"] = [{"status": "completed"}] * 12
        tools = RecoveryTools(self.run, self.journal)
        with self.assertRaises(Stop) as error:
            tools.dispatch("harness_read", {"file": "go/import.go"})
        self.assertEqual(error.exception.outcome, "budget_exhausted")
        self.journal.state["resumes"] = 3
        with self.assertRaises(Stop) as error:
            execute(self.run, self.journal, "retry")
        self.assertEqual(error.exception.outcome, "budget_exhausted")

    def test_only_one_resume_can_own_the_session(self):
        with lock_session(self.run.path):
            with self.assertRaises(Stop) as error:
                lock_session(self.run.path)
        self.assertIn("already has an owner", error.exception.reason)
        with lock_session(self.run.path):
            pass

    def test_individual_edits_are_partial_and_are_never_rolled_back(self):
        first = self.tools.prepared_calls("retry")
        for call in first[:3]:
            self.tools.dispatch(call["name"], call["args"])
        first_patch = (self.checkout / "go/import.go").read_bytes()
        self.journal.fault = "before-edit"
        for call in self.tools.prepared_calls("retry")[:1]:
            self.tools.dispatch(call["name"], call["args"])
        call = first[4]
        with self.assertRaises(Stop):
            self.tools.dispatch(call["name"], call["args"])
        self.assertEqual((self.checkout / "go/import.go").read_bytes(), first_patch)
        self.assertEqual([i["status"] for i in self.journal.state["operations"].values()], ["completed", "intent"])


if __name__ == "__main__":
    unittest.main()
