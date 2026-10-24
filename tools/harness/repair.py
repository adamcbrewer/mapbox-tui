import json
from pathlib import Path
import re
import signal
import subprocess
import tarfile
import time

from container import ContainerRun
from pi_run import Tools, scripted_calls
from run import REPORTS, Stop, digest, now, write_json
from verify import REQUIRED_CHECKS, SCOPE, VerificationRun, check_evaluator, evaluator_receipt_matches, freeze_evaluator, receipt_matches, verify

MAX_ATTEMPTS = 2


class RepairRun(ContainerRun):
    def __init__(self, reports, image):
        super().__init__(reports, image)
        self.result.update(attempts=[], model_claim=None, fully_verified=False,
                           verification_scope=SCOPE, repair_budget_exhausted=False,
                           repair_runner_sha256=digest(Path(__file__).read_bytes()))
        freeze_evaluator(self)

    def event(self, kind, **fields):
        identity = {key: self.result.get(key) for key in
                    ("attempt", "patch_sha256", "base_commit", "task_sha256", "image", "evaluator_sha256")}
        if "check" in fields:
            fields["check"].update(identity, run_id=self.path.name)
        super().event(kind, **identity, **fields)
        write_json(self.path / "result.json", self.result)


class RepairTools(Tools):
    def __init__(self, run, checkout):
        super().__init__(run, checkout)
        self.attempts = run.result["attempts"]
        self.terminal = False
        self.start_context = {"verification": {"maxAttempts": MAX_ATTEMPTS, "scope": SCOPE}}

    def edit_available(self):
        return not self.terminal and self.edits == len(self.attempts) < MAX_ATTEMPTS

    def check_available(self):
        return not self.terminal and self.edits == len(self.attempts) + 1 and len(self.attempts) < MAX_ATTEMPTS

    def completed(self):
        return self.checked and self.edits == len(self.attempts)

    def prepared_calls(self, demo):
        first = scripted_calls(self, "wrong")[3]["args"]
        wrong = self.seeded.replace(first["old"], first["new"], 1)
        start = wrong.index('\tformat := "fit"')
        end = wrong.index('\t\td := xml.NewDecoder(reader)') + len('\t\td := xml.NewDecoder(reader)')
        old = wrong[start:end]
        good = scripted_calls(self, "good")[3]["args"]
        new = old.replace('"fit"', '"FIT"', 1).replace(good["old"], good["new"], 1)
        second = {"file": "go/import.go", "operation_id": "repair-2", "expected_sha256": digest(wrong.encode()),
                  "old": old if demo == "retry" else '"fit"', "new": new if demo == "retry" else '"Fit"'}
        read = {"name": "harness_read", "args": {"file": "go/import.go"}}
        check = {"name": "harness_check", "args": {"preset": "xml-leading-bom"}}
        return [read, {"name": "harness_edit", "args": first}, check,
                read, {"name": "harness_edit", "args": second}, check]

    def check_candidate(self, result):
        number = len(self.attempts) + 1
        patch = (self.run.path / "candidate.patch").read_bytes()
        child = VerificationRun(REPORTS / "verification", self.run.image, number, self.run.path.name,
                                evaluator=self.run.result["evaluator"])
        child.deadline = self.run.deadline
        child.recovery_checkpoint = getattr(self.run, "recovery_checkpoint", None)
        child.result["candidate"] = "repair-attempt"
        attempt = {"attempt": number, "verification_run_id": child.path.name,
                   "patch_sha256": digest(patch), "evaluator_sha256": child.result["evaluator_sha256"],
                   "outcome": "incomplete", "started_at": now()}
        self.attempts.append(attempt)
        self.run.event("verification_started", verification=attempt)
        handlers = {s: signal.signal(s, child.cancel) for s in (signal.SIGINT, signal.SIGTERM)}
        try:
            child.event("run_started")
            verify(child, patch)
        except Stop as error:
            child.result.update(outcome=error.outcome, reason=error.reason)
        except KeyboardInterrupt:
            child.result.update(outcome="cancelled", reason="Independent verification was interrupted")
            raise
        except (OSError, ValueError, subprocess.TimeoutExpired, tarfile.TarError) as error:
            child.result.update(outcome="blocked", reason=f"Independent verification could not complete: {error}")
        finally:
            child.finalizing = True
            try:
                child.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - child.started, 4))
                child.event("run_finished", outcome=child.result["outcome"])
                attempt.update(outcome=child.result["outcome"], ended_at=child.result["ended_at"],
                               duration_seconds=child.result["duration_seconds"],
                               receipt_sha256=digest((child.path / "result.json").read_bytes()))
                self.run.event("verification_finished", verification=attempt)
            finally:
                for signum, handler in handlers.items():
                    signal.signal(signum, handler)
        outcome = child.result["outcome"]
        passed = outcome == "repository_verification_passed"
        failed = outcome == "repair_failed"
        if not passed and not failed:
            self.terminal = True
            raise Stop(outcome, child.result.get("reason", "Independent verification could not complete"))
        if (child.result["verification_scope"] != SCOPE or
                child.result["evaluator_sha256"] != self.run.result["evaluator_sha256"] or
                any(child.result.get(key) != self.run.result.get(key) for key in ("base_commit", "task_sha256", "image")) or
                not receipt_matches(child.result, child.path, allow_failed=failed)):
            self.terminal = True
            raise Stop("invalid_input", "Independent verification evidence or task identity did not match")
        self.terminal = passed or number == MAX_ATTEMPTS
        remaining = MAX_ATTEMPTS - number if failed else 0
        checks = [{key: check.get(key) for key in ("id", "outcome", "exit_code", "stdout", "stderr")}
                  for check in child.result["checks"] if check["id"] in REQUIRED_CHECKS]
        failure = checks[-1] if failed else None
        diagnostics = {}
        if failure:
            diagnostics = {stream: (child.path / failure[stream]).read_text(errors="replace")[-4000:]
                           for stream in ("stdout", "stderr")}
        result.update(outcome="passed" if passed else "repair_failed",
                      explanation="The exact patch passed every independent check" if passed else child.result["reason"],
                      evidence=f"verification/runs/{child.path.name}/result.json",
                      attempt=number, remaining_attempts=remaining, patch_sha256=attempt["patch_sha256"],
                      verification_run_id=child.path.name, checks=checks, diagnostics=diagnostics)
        self.run.event("verification_feedback", feedback=result.copy())
        if remaining:
            self.run.result.update(attempt=number + 1, patch_sha256=None)

    def finish(self):
        check_evaluator(self.run)
        if not self.completed() or not self.attempts:
            raise Stop("incomplete", "Pi did not finish an edited candidate and its independent verification")
        final = self.attempts[-1]
        if final["outcome"] == "repository_verification_passed":
            self.run.result.update(outcome="verified_repair_passed", patch_sha256=final["patch_sha256"],
                                   reason="The final exact repair passed all independent checks; human review remains pending")
            if not repair_receipt_matches(self.run.result, self.run.path, REPORTS):
                raise Stop("invalid_input", "Final patch or independent verification evidence no longer matches")
        elif len(self.attempts) == MAX_ATTEMPTS:
            self.run.result.update(outcome="budget_exhausted", repair_budget_exhausted=True)
            if not repair_receipt_matches(self.run.result, self.run.path, REPORTS):
                raise Stop("invalid_input", "Failed-attempt verification evidence no longer matches")
            raise Stop("budget_exhausted", "Both proposed repairs failed independent checks; no third attempt is allowed")
        else:
            raise Stop("incomplete", "Pi stopped after a failed check without completing its permitted retry")


def repair_receipt_matches(result, directory, reports):
    try:
        attempts = result["attempts"]
        task_bytes = (directory / "task.json").read_bytes()
        task = json.loads(task_bytes)
        if (result["workflow"] != "pi-verified-repair" or result["run_id"] != directory.name or
                result["verification_scope"] not in ("permitted-files-v1", SCOPE) or result["attempt"] != len(attempts) or
                result["budgets"]["repair_attempts"] != MAX_ATTEMPTS or not 1 <= len(attempts) <= MAX_ATTEMPTS or
                result["task_sha256"] != digest(task_bytes) or
                result["base_commit"] != task["base_commit"] or
                result["patch_sha256"] != digest((directory / "candidate.patch").read_bytes()) or
                result["patch_sha256"] != attempts[-1]["patch_sha256"]):
            return False
        if result["verification_scope"] == SCOPE and not evaluator_receipt_matches(result, task):
            return False
        for number, item in enumerate(attempts, 1):
            identity = item["verification_run_id"]
            if not isinstance(identity, str) or not re.fullmatch(r"[0-9a-f]{32}", identity):
                return False
            path = reports / "verification/runs" / identity
            raw = (path / "result.json").read_bytes()
            child = json.loads(raw)
            failed = child["outcome"] == "repair_failed"
            if (item["attempt"] != number or child["attempt"] != number or
                    child["parent_run_id"] != result["run_id"] or child["outcome"] != item["outcome"] or
                    child["verification_scope"] != result["verification_scope"] or item["receipt_sha256"] != digest(raw) or
                    result["verification_scope"] == SCOPE and child["evaluator_sha256"] != result["evaluator_sha256"] or
                    any(child[key] != result[key] for key in ("base_commit", "task_sha256", "image")) or
                    child["patch_sha256"] != item["patch_sha256"] or
                    not receipt_matches(child, path, allow_failed=failed) or
                    number < len(attempts) and not failed):
                return False
        return (attempts[-1]["outcome"] == "repository_verification_passed" if result["outcome"] == "verified_repair_passed" else
                result["outcome"] == "budget_exhausted" and result["repair_budget_exhausted"] is True and
                len(attempts) == MAX_ATTEMPTS and all(item["outcome"] == "repair_failed" for item in attempts))
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        return False
