import json
import signal
import subprocess
import tarfile
import time

from run import REPORTS, Stop, digest, now
from task_inputs import definition, materials
from verify import REQUIRED_CHECKS, VerificationRun, check_evaluator, fingerprint, freeze_evaluator, receipt_matches, verify


def prepare_verification(run, case):
    run.task = definition(case["id"])
    run.test_command = run.task["command"]
    if case["id"] != run.task["id"] or case["base_commit"] != run.task["base_commit"]:
        raise Stop("invalid_input", "This checker only supports the pinned development task")
    inputs = materials(run.task)
    if digest(inputs["regression"]) != case["private_files_sha256"]["verifier.go.txt"]:
        raise Stop("invalid_input", "The private checking examples don't match this task")
    if case.get("seed_sha256", run.task["seed_sha256"]) != run.task["seed_sha256"]:
        raise Stop("invalid_input", "The planted change doesn't match this task")
    freeze_evaluator(run)
    task_bytes = inputs["task"]
    (run.path / "verification-task.json").write_bytes(task_bytes)
    run.result.update(independent_checks_passed=False, base_commit=case["base_commit"],
                      task_sha256=digest(task_bytes),
                      verification_seed_tree_sha256=fingerprint({name: value for name, value in case["agent_files_sha256"].items()
                                                                 if name != "task.json"}),
                      verification_regression_sha256=case["private_files_sha256"]["verifier.go.txt"])


def verification_matches(run, child):
    try:
        result = child.result
        failed = result["outcome"] == "repair_failed"
        return (result["outcome"] in ("repository_verification_passed", "repair_failed") and
                result["parent_run_id"] == run.path.name and bool(result["containers"]) and
                all(container["removed"] for container in result["containers"]) and
                all(result[key] == run.result[key] for key in
                    ("image", "base_commit", "task_sha256", "patch_sha256", "evaluator_sha256")) and
                result["trees"]["seed"]["sha256"] == run.result["verification_seed_tree_sha256"] and
                result["regression_sha256"] == run.result["verification_regression_sha256"] and
                result["trees"]["candidate"]["files"]["go/import.go"] == run.result["snapshot_source_sha256"] and
                receipt_matches(result, child.path, allow_failed=failed))
    except (OSError, ValueError, KeyError, TypeError):
        return False


def check_candidate(run, attempt=1):
    run.result["independent_checks_passed"] = False
    if run.result.get("verification"):
        raise Stop("budget_exhausted", "This prepared run permits one independent check")
    if type(attempt) is not int or attempt not in (1, 2):
        raise Stop("invalid_input", "Only two saved repair attempts are permitted")
    if run.cancel_requested:
        raise KeyboardInterrupt
    if run.deadline <= time.monotonic():
        raise Stop("timeout", "There is no time left for independent checking")
    check_evaluator(run)
    patch = (run.path / "candidate.patch").read_bytes()
    source = (run.path / "candidate-source.bin").read_bytes()
    snapshot = json.loads((run.path / "project-snapshot.json").read_text())
    if (run.result.get("snapshot_scope_valid") is not True or
            not run.result["containers"] or not all(container["removed"] for container in run.result["containers"]) or
            digest(patch) != run.result["patch_sha256"] or
            digest(source) != run.result["snapshot_source_sha256"] or
            not isinstance(snapshot, dict) or not isinstance(snapshot.get("files"), dict) or
            snapshot["files"].get("go/import.go") != digest(source)):
        raise Stop("invalid_input", "The saved change or sandbox cleanup doesn't match its evidence")
    child = VerificationRun(REPORTS / "verification", run.image, attempt=attempt, parent_run_id=run.path.name,
                            evaluator=run.result["evaluator"], task=run.task)
    child.deadline = run.deadline
    child.result["candidate"] = "normal-pi-saved-patch"
    record = {"run_id": child.path.name, "outcome": "incomplete", "patch_sha256": digest(patch),
              "checks": [], "containers_removed": 0, "containers_created": 0}
    run.result["verification"] = record
    run.event("verification_started", verification=record)
    handlers = {signum: signal.signal(signum, child.cancel) for signum in (signal.SIGINT, signal.SIGTERM)}
    try:
        child.event("run_started")
        verify(child, patch)
    except Stop as error:
        child.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        child.result.update(outcome="cancelled", reason="Independent checking was interrupted")
        raise
    except (OSError, ValueError, subprocess.TimeoutExpired, tarfile.TarError) as error:
        child.result.update(outcome="blocked", reason=f"Independent checking couldn't finish: {error}")
    finally:
        child.finalizing = True
        try:
            for check in child.result["checks"]:
                for stream in ("stdout", "stderr"):
                    if check.get(stream) and (child.path / check[stream]).is_file():
                        check.setdefault(stream + "_sha256", digest((child.path / check[stream]).read_bytes()))
            child.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - child.started, 4))
            child.event("run_finished", outcome=child.result["outcome"])
            record.update(outcome=child.result["outcome"], reason=child.result.get("reason"),
                          receipt_sha256=digest((child.path / "result.json").read_bytes()),
                          duration_seconds=child.result["duration_seconds"],
                          checks=[{key: check.get(key) for key in ("id", "outcome", "exit_code")}
                                  for check in child.result["checks"] if check["id"] in REQUIRED_CHECKS],
                          containers_removed=sum(container["removed"] for container in child.result["containers"]),
                          containers_created=len(child.result["containers"]))
            run.event("verification_finished", verification=record)
        finally:
            for signum, handler in handlers.items():
                signal.signal(signum, handler)
    check_evaluator(run)
    if child.result["outcome"] not in ("repository_verification_passed", "repair_failed"):
        raise Stop(child.result["outcome"], child.result.get("reason", "Independent checking couldn't finish"))
    saved = (child.path / "result.json").read_bytes()
    if (not verification_matches(run, child) or digest(saved) != record["receipt_sha256"] or json.loads(saved) != child.result or
            digest((run.path / "candidate.patch").read_bytes()) != record["patch_sha256"] or
            digest((run.path / "candidate-source.bin").read_bytes()) != run.result["snapshot_source_sha256"]):
        raise Stop("invalid_input", "The independent checks don't match the saved patch, task or rules")
    if child.result["outcome"] == "repair_failed":
        raise Stop("repair_failed", child.result["reason"])
    if time.monotonic() > run.deadline:
        raise Stop("timeout", "Independent checking and cleanup exceeded the run's overall deadline")
    run.result.update(outcome="native_verification_passed", independent_checks_passed=True,
                      reason="The saved normal-Pi patch passed every independent check; my review is still pending")
    run.event("native_patch_verified")
