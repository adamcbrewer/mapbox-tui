import argparse
import json
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import tarfile
import tempfile
import time

from pi_run import MAX_CALLS, ModelRequestLimit, load_live_config, pi_loop, prepare_checkout, repository_instructions, token_summary
from recovery import FAULTS, Journal, RecoveryTools, durable_json, lock_session, process_identity, publication_lock, request_cancel, validate_entries
from repair import RepairRun, repair_receipt_matches
from run import REPORTS, Stop, now
from sandbox import preflight


def restore_run(directory):
    try:
        state = json.loads((directory / "journal.json").read_text())
        observed = json.loads((directory / "result.json").read_text())
        if state["result"] != observed:
            raise ValueError("Journal and latest receipt disagree; existing evidence was retained")
        run = RepairRun.__new__(RepairRun)
        run.path, run.started = directory, time.monotonic()
        run.deadline = run.started + max(0, state["expires_at"] - time.time())
        run.result = state["result"]
        run.image = run.result["image"]
        run.task = json.loads((directory / "task.json").read_text())
        run.test_command = run.task["command"]
        run.seccomp, run.prepared = None, False
        run.hooks = directory / "empty-hooks"
        run.launching = run.finalizing = run.cancel_requested = False
        return run
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        raise Stop("incomplete", f"Missing or inconsistent session state; deliberate reconstruction is required: {error}") from error


def execute(run, journal, demo, fault=None):
    journal.fault = fault
    journal.checkpoint("resume")
    journal.reconcile()
    if journal.state["status"] == "complete":
        RecoveryTools(run, journal).finish()
        return
    if journal.state["status"] not in ("active", "incomplete"):
        raise Stop(journal.state["status"], "This session is stopped; inspect its handoff before starting a new bounded task")
    if journal.state["resumes"] >= 3:
        raise Stop("budget_exhausted", "At most three recovery entries are allowed; inspect the retained state")
    journal.state["resumes"] += 1
    journal.record("recovery_entered", entry=journal.state["resumes"])
    tools = RecoveryTools(run, journal)
    tools.recover_pending()
    tools.refresh_context()
    config = journal.state.get("live_config")
    run.repo_instructions = repository_instructions(run.task, journal.checkout)
    if tools.completed() and tools.terminal:
        tools.finish()
        return
    preflight(run)
    run.result["outcome"] = "incomplete"
    run.prepare()
    entry = journal.state["resumes"]
    archive_pi(run.path, entry - 1)
    try:
        if config and len(journal.state["model_reservations"]) == config["maxModelRequests"]:
            raise ModelRequestLimit("Persisted model-request budget reached")
        with tempfile.TemporaryDirectory(prefix="mapbox-pi-") as temporary:
            pi_loop(run, tools, Path(temporary), demo, config)
    except ModelRequestLimit:
        if not config or len(journal.state["model_reservations"]) != config["maxModelRequests"]:
            raise
        if tools.check_available():
            journal.record("final_verification_requested", request=tools.calls + 1,
                           origin="runner", reason="model_requests_exhausted")
            tools.dispatch("harness_check", {"preset": "xml-leading-bom"})
        if not tools.completed() or not tools.terminal:
            raise
    finally:
        archive_pi(run.path, entry)
    tools.finish()


def archive_pi(directory, entry):
    target = directory / "pi-sessions" / str(entry)
    target.mkdir(parents=True, exist_ok=True)
    for name in ("context.json", "pi-session.json", "pi.stderr"):
        path = directory / name
        if path.is_file():
            shutil.copyfile(path, target / name)


def finalize(run, journal):
    with publication_lock(run.path):
        run.finalizing = True
        try:
            if (run.path / "cancel.json").exists():
                raise Stop("cancelled", "Cancellation recorded; no successful receipt is published")
            if journal and run.result["outcome"] == "verified_repair_passed":
                journal.checkpoint("publication")
                journal.reconcile()
                if not repair_receipt_matches(run.result, run.path, REPORTS):
                    raise Stop("incomplete", "Verification evidence changed before publication")
        except (Stop, OSError, ValueError, KeyError, TypeError, AttributeError) as error:
            run.result.update(outcome=error.outcome if isinstance(error, Stop) else "incomplete",
                              reason=error.reason if isinstance(error, Stop) else f"Cannot confirm final evidence: {error}")
        if journal and journal.state.get("live_config"):
            reserved = len(journal.state["model_reservations"])
            try:
                validate_entries({**journal.state, "result": run.result}, run.task)
                run.result["token_summary"] = token_summary(run.result.get("provider_usage", []), reserved)
                run.result["token_summary"].update(requests_reserved=reserved,
                                                  observed_dispatches=run.result.get("model_requests_dispatched", 0))
            except (Stop, TypeError, ValueError, KeyError):
                run.result.update(outcome="incomplete", reason="Model usage could not be reconciled; inspect retained evidence",
                                  token_summary={"complete": False, "requests_reserved": reserved})
        run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
        if journal:
            journal.state["status"] = "complete" if run.result["outcome"] == "verified_repair_passed" else run.result["outcome"]
            journal.record("session_stopped", outcome=run.result["outcome"])
            journal.handoff(run.result.get("reason") if journal.state["status"] != "complete" else None)
        durable_json(run.path / "result.json", run.result)


def main():
    parser = argparse.ArgumentParser(description="Recover a bounded prepared or subscription Pi repair from trusted local state.")
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--resume", help="Existing recovery run ID; always constructs fresh Pi context")
    action.add_argument("--cancel", help="Request cancellation of an existing recovery run")
    parser.add_argument("--image", help="Immutable prepared local image ID, for a new run")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--demo", choices=("retry", "exhausted"))
    mode.add_argument("--live", action="store_true", help="Use the saved local subscription model and limits")
    parser.add_argument("--fault", choices=FAULTS)
    args = parser.parse_args()
    directory = None
    owner = None
    journal = None
    previous = {}
    run = None
    try:
        identity = args.resume or args.cancel
        if identity:
            if args.live or args.demo or args.image:
                raise Stop("invalid_input", "Resume/cancel use the original saved mode, model and limits")
            if not re.fullmatch(r"[0-9a-f]{32}", identity):
                raise Stop("invalid_input", "Use a recovery run ID, not a path")
            directory = REPORTS / "recovery/runs" / identity
            if not directory.is_dir() or directory.is_symlink():
                raise Stop("incomplete", "Saved recovery session is missing")
        if args.cancel:
            requested = request_cancel(directory)
            print(f"{'Cancellation requested' if requested else 'Session already stopped'} for {args.cancel}; files and logs are retained")
            return 0
        if args.resume:
            owner = lock_session(directory)
            run = restore_run(directory)
            journal = Journal(run)
            args.demo = journal.state["demo"]
        else:
            if not args.image:
                raise Stop("invalid_input", "A new recovery session needs --image")
            args.demo = args.demo or "retry"
            config = load_live_config() if args.live else None
            run = RepairRun(REPORTS / "recovery", args.image)
            directory = run.path
            owner = lock_session(directory)
            run.result.update(workflow="pi-verified-repair", recovery=True, candidate=args.demo,
                              live_config=config, recovery_demo=args.demo,
                              model="scripted (no AI)", pi_version="1.2.0", startup_verified=False,
                              budgets={"tool_calls": MAX_CALLS, "repair_attempts": 2, "deadline_seconds": 600,
                                       "model_requests": 0, "transport_retries": 0, "recovery_entries": 3})
            if config:
                run.result.update(candidate="model", candidate_source="AI", provider=config["provider"], model=config["model"],
                                  auth=config["auth"], reasoning="low", model_cost_usd=None, model_requests=0,
                                  model_requests_dispatched=0, model_usage=[], provider_usage=[])
                run.result["budgets"].update(model_requests=config["maxModelRequests"],
                                           output_tokens_per_request=config["maxOutputTokensPerRequest"])
        previous = {s: signal.signal(s, run.cancel) for s in (signal.SIGINT, signal.SIGTERM)}
        durable_json(directory / "owner.json", {"pid": os.getpid(), "identity": process_identity(os.getpid())})
        if journal is None:
            preflight(run)
            run.result["outcome"] = "incomplete"
            run.prepare()
            checkout = Path(tempfile.mkdtemp(prefix="mapbox-calibration-recovery-"))
            run.result["retained_checkout"] = str(checkout)
            prepare_checkout(run, checkout)
            journal = Journal(run, checkout)
            journal.state.update(demo=args.demo, live_config=config, model_reservations=[])
            journal.save()
        execute(run, journal, args.demo, args.fault)
        journal.state["status"] = "complete"
    except Stop as error:
        if run:
            run.result.update(outcome=error.outcome, reason=error.reason)
        else:
            if directory and owner:
                durable_json(directory / "recovery-stop.json", {"outcome": "incomplete", "at": now(),
                                                               "reason": error.reason, "evidence_retained": True})
            print(f"{error.outcome}: {error.reason}")
            return 1
        if journal:
            journal.state["status"] = error.outcome
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Stopped; partial files and logs are retained")
        if journal:
            journal.state["status"] = "cancelled"
    except (OSError, ValueError, KeyError, TypeError, AttributeError, subprocess.TimeoutExpired, tarfile.TarError) as error:
        if run is None:
            print(f"incomplete: Cannot reconstruct the session: {error}")
            return 1
        run.result.update(outcome="blocked", reason=f"Cannot complete recovery: {error}")
        if journal:
            journal.state["status"] = "blocked"
    finally:
        if run and owner:
            if args.resume and journal is None:
                durable_json(run.path / "recovery-stop.json", {"outcome": "incomplete", "at": now(),
                                                             "reason": run.result.get("reason"), "evidence_retained": True})
            else:
                finalize(run, journal)
            (run.path / "owner.json").unlink(missing_ok=True)
        for signum, handler in previous.items():
            signal.signal(signum, handler)
        if owner:
            owner.close()
    print(f"{run.result['outcome']}: {run.result.get('reason', 'See evidence')}")
    print(run.path / "result.json")
    if journal:
        print(f"Retained working copy: {journal.checkout}")
        print("Fresh Pi context; subscription model." if journal.state.get("live_config") else
              "Fresh Pi context; prepared replies only, no AI model.")
        print("Human review remains pending.")
    return 0 if journal and journal.state["status"] == "complete" else 1


if __name__ == "__main__":
    raise SystemExit(main())
