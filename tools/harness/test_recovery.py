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
