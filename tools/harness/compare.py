import argparse
from collections import Counter
import json
from pathlib import Path
import re
import signal
import tempfile
import time
import uuid

from container import ContainerRun, ENVIRONMENT
from evaluate import local_evidence, record
from pi_run import MAX_CALLS, prepare_checkout, scripted_calls
from recover import execute, finalize
from recovery import Journal, RecoveryTools, durable_json, lock_session
from repair import RepairRun
from run import HERE, REPORTS, ROOT, Stop, digest, now
from sandbox import preflight
from verify import evaluator_definition, fingerprint, receipt_matches

ARMS = {"stop": False, "automatic": True}
CONFIG = {"provider": "openai", "model": "prepared-budget-simulation", "auth": "subscription",
          "maxModelRequests": 10, "maxOutputTokensPerRequest": None}
BUDGETS = {"tool_calls": MAX_CALLS, "repair_attempts": 2, "deadline_seconds": 600,
           "model_requests": 10, "output_tokens_per_request": None, "transport_retries": 0, "recovery_entries": 3}


def sources():
    paths = [path for path in HERE.glob("*.py") if not path.name.startswith("test_")]
    paths += list((HERE / "pi").glob("*.mjs")) + [HERE / "pi/package-lock.json", HERE / "task.json"]
    paths += [HERE / "fixtures" / (case + ".patch") for case in ("good", "wrong")]
    return {str(path.relative_to(HERE)): digest(path.read_bytes()) for path in sorted(paths)}


def prepare(image, repeats):
    directory = REPORTS / "compare/experiments" / uuid.uuid4().hex
    directory.mkdir(parents=True)
    run = ContainerRun(REPORTS / "comparison-preflight", image)
    try:
        preflight(run)
        run.prepare()
        check, output = run.command("go-version", ["go", "version"], ROOT)
        plan = {"schema_version": 1, "created_at": now(), "experiment": "automatic-final-verification",
                "primary_metric": "verified delivery within the original run, without another model request",
                "reason": "Recover #106 retained a final edit after model requests ran out, before it was checked.",
                "synthetic": True, "model_execution": "none; request exhaustion is simulated",
                "image": image, "go_version": output.decode().strip(), "runtime": run.result["runtime"],
                "environment": run.result["environment"], "container_environment": ENVIRONMENT,
                "seccomp_sha256": run.result["seccomp_sha256"], "sources": sources(),
                "evaluator": evaluator_definition(run.task), "config": CONFIG, "budgets": BUDGETS,
                "arms": ARMS, "repeats": repeats,
                "cases": {case: {"patch_sha256": digest((HERE / "fixtures" / (case + ".patch")).read_bytes()),
                                 "expected_check": "passed" if case == "good" else "repair_failed"}
                          for case in ("good", "wrong")},
                "schedule": [{"case": case, "repeat": repeat + 1, "arm": arm}
                             for repeat in range(repeats) for case in ("good", "wrong")
                             for arm in (list(ARMS) if repeat % 2 == 0 else list(reversed(ARMS)))]}
        for name in plan["sources"]:
            target = directory / "inputs" / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((HERE / name).read_bytes())
        durable_json(directory / "plan.json", {"sha256": fingerprint(plan), "definition": plan})
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
        raise
    finally:
        run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
        durable_json(run.path / "result.json", run.result)
    return directory


def load_plan(directory):
    saved = json.loads((directory / "plan.json").read_text())
    plan = saved["definition"]
    if (fingerprint(plan) != saved["sha256"] or plan["sources"] != sources() or
            plan["config"] != CONFIG or plan["budgets"] != BUDGETS or plan["arms"] != ARMS or
            any(digest((directory / "inputs" / name).read_bytes()) != expected for name, expected in plan["sources"].items())):
        raise ValueError("Plan or harness inputs changed; retain this experiment and prepare a new one")
    return plan


def run_cell(plan, cell, progress, directory):
    run = RepairRun(REPORTS / "recovery", plan["image"])
    cell.update(receipt=f"recovery/runs/{run.path.name}/result.json", status="started")
    durable_json(directory / "progress.json", progress)
    journal = None
    owner = lock_session(run.path)
    handlers = {s: signal.signal(s, run.cancel) for s in (signal.SIGINT, signal.SIGTERM)}
    run.result.update(workflow="pi-verified-repair", recovery=True, candidate=cell["case"],
                      candidate_source="prepared", demonstration="simulated-model-limit-real-checks",
                      comparison=cell.copy(), automatic_final_check=ARMS[cell["arm"]],
                      live_config=CONFIG.copy(), provider=CONFIG["provider"], model=CONFIG["model"], auth=CONFIG["auth"],
                      model_requests=0, model_requests_dispatched=0, provider_usage=[], model_usage=[],
                      actual_model_calls=0, budgets=BUDGETS.copy())
    try:
        preflight(run)
        run.result["outcome"] = "incomplete"
        run.prepare()
        if any(run.result[key] != plan[key] for key in ("runtime", "environment", "seccomp_sha256")):
            raise Stop("blocked", "Observed environment differs from the frozen comparison plan")
        checkout = Path(tempfile.mkdtemp(prefix="mapbox-calibration-comparison-"))
        run.result["retained_checkout"] = str(checkout)
        prepare_checkout(run, checkout)
        journal = Journal(run, checkout)
        tools = RecoveryTools(run, journal)
        read = tools.dispatch("harness_read", {"file": "go/import.go"})
        edit = scripted_calls(tools, cell["case"])[3]["args"]
        edit.pop("expected_sha256")
        edit["read_id"] = read["read_id"]
        result = tools.dispatch("harness_edit", edit)
        if result["outcome"] != "ok":
            raise Stop("invalid_input", "Prepared edit was refused; inspect the saved evidence")
        for request in range(1, CONFIG["maxModelRequests"] + 1):
            tools.reserve_model({"request": request, "provider": CONFIG["provider"], "model": CONFIG["model"]})
        journal.record("model_limit_simulated", actual_model_calls=0)
        execute(run, journal, "retry")
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Comparison stopped; partial evidence retained")
        raise
    except (OSError, ValueError, KeyError, TypeError) as error:
        run.result.update(outcome="blocked", reason=str(error))
    finally:
        try:
            finalize(run, journal)
            cell.update(status="finished", receipt_sha256=digest((run.path / "result.json").read_bytes()))
            durable_json(directory / "progress.json", progress)
        finally:
            for signum, handler in handlers.items():
                signal.signal(signum, handler)
            owner.close()


def summarize(directory):
    saved = json.loads((directory / "plan.json").read_text())
    plan = saved["definition"]
    if fingerprint(plan) != saved["sha256"]:
        raise ValueError("Comparison plan fingerprint does not match")
    progress = json.loads((directory / "progress.json").read_text())
    if (progress["plan_sha256"] != saved["sha256"] or
            [{key: cell[key] for key in ("case", "repeat", "arm")} for cell in progress["cells"]] != plan["schedule"]):
        raise ValueError("Progress no longer matches the frozen schedule")
    rows = []
    for cell in progress["cells"]:
        row = {**cell, "verified_success": False, "unconfirmed_completion": False, "outcome": "not_recorded"}
        if cell.get("receipt"):
            path = REPORTS / cell["receipt"]
            try:
                if (not re.fullmatch(r"recovery/runs/[0-9a-f]{32}/result.json", cell["receipt"]) or
                        not local_evidence(path.parent, REPORTS) or cell.get("receipt_sha256") != digest(path.read_bytes())):
                    raise ValueError("Receipt changed or was interrupted before sealing")
                observed = record(path, REPORTS)
                row.update(observed)
                result = observed["receipt_snapshot"]
                row["candidate_sha256"] = digest((path.parent / "candidate.patch").read_bytes())
                if (any(result["comparison"][key] != cell[key] for key in ("case", "repeat", "arm")) or
                        result["automatic_final_check"] is not plan["arms"][cell["arm"]] or
                        result["live_config"] != plan["config"] or result["budgets"] != plan["budgets"] or
                        result["evaluator"] != plan["evaluator"] or
                        any(result[key] != plan[key] for key in ("image", "runtime", "environment", "seccomp_sha256")) or
                        result.get("patch_sha256") is not None and result["patch_sha256"] != row["candidate_sha256"]):
                    raise ValueError("Run settings or candidate differ from the frozen plan")
                for attempt in result["attempts"]:
                    identity = attempt["verification_run_id"]
                    if not isinstance(identity, str) or not re.fullmatch(r"[0-9a-f]{32}", identity):
                        raise ValueError("Invalid independent-check identity")
                    child_path = REPORTS / "verification/runs" / identity
                    if not local_evidence(child_path, REPORTS):
                        raise ValueError("Independent-check evidence is missing or linked")
                    raw = (child_path / "result.json").read_bytes()
                    child = json.loads(raw)
                    if (digest(raw) != attempt["receipt_sha256"] or child["parent_run_id"] != result["run_id"] or
                            child["outcome"] != attempt["outcome"] or child["patch_sha256"] != attempt["patch_sha256"] or
                            child["evaluator_sha256"] != fingerprint(plan["evaluator"]) or
                            not receipt_matches(child, child_path, allow_failed=child["outcome"] == "repair_failed")):
                        raise ValueError("Independent-check evidence no longer matches, including failed checks")
            except (OSError, ValueError, KeyError, TypeError) as error:
                row.update(verified_success=False, problem=str(error), unconfirmed_completion=True)
                if row["outcome"] == "not_recorded":
                    row["outcome"] = "evidence_unreadable"
        rows.append(row)
    for row in rows:
        paired = [other for other in rows if (other["case"], other["repeat"]) == (row["case"], row["repeat"])]
        patches = {other["candidate_sha256"] for other in paired if "candidate_sha256" in other}
        if len(patches) > 1:
            row.update(verified_success=False, unconfirmed_completion=True, problem="Paired repairs differ")
        if row["case"] == "wrong" and row["verified_success"]:
            row.update(verified_success=False, unconfirmed_completion=True, problem="Known wrong repair counted as a success")
    totals = {arm: {"planned": sum(cell["arm"] == arm for cell in plan["schedule"]),
                    "verified_deliveries": sum(row["verified_success"] for row in rows if row["arm"] == arm),
                    "recorded_duration_seconds": round(sum(row.get("duration_seconds") or 0 for row in rows if row["arm"] == arm), 4),
                    "runs_with_duration": sum(row.get("duration_seconds") is not None for row in rows if row["arm"] == arm),
                    "outcomes": dict(Counter(row["outcome"] for row in rows if row["arm"] == arm))}
              for arm in ARMS}
    result = {"plan_sha256": saved["sha256"], "generated_at": now(), "rows": rows, "totals": totals,
              "promotion_blocked": any(row["unconfirmed_completion"] or row["outcome"] == "not_recorded" for row in rows)}
    durable_json(directory / "comparison.json", result)
    (directory / "comparison.md").write_text(render_comparison(plan, result))
    return result


def render_comparison(plan, result):
    rows, totals = result["rows"], result["totals"]
    lines = ["# Does finishing checks help after model requests run out?", "",
             "I tested a problem from #106: a fix had been written, but the AI had run out of turns before asking for tests.",
             "I compared stopping there with letting our code finish the checks. I saved the plan before running either version.",
             "The script supplied the same working fix and deliberately bad fix to both versions, with the same limits and tests.",
             "I ran each pair twice and reversed their order the second time. Only automatic checking changed.",
             "No repair AI or Pi session ran. I simulated the request limit; the Go checks ran in real containers.",
             "I counted a success only when the run finished with a fix that passed every required check within its original limits.",
             "This tests whether our code can finish the job. I haven't measured better AI repairs or time saved for me.", ""]
    for arm, total in totals.items():
        label = "Our code finishes the checks" if ARMS[arm] else "Stop when the AI has no turns left"
        lines.extend([f"## {label}", "", f"Finished with a fully checked fix: {total['verified_deliveries']}/{total['planned']} planned attempts.", ""])
        lines.extend(f"- {outcome}: {count}" for outcome, count in total["outcomes"].items())
        lines.extend(["", f"Time across {total['runs_with_duration']} runs: {total['recorded_duration_seconds']} seconds, including failed attempts and checks.", ""])
    lines.extend(["| Fix I supplied | Stop: fully checked fixes | Automatic: fully checked fixes |",
                  "| --- | --- | --- |"])
    for case in plan["cases"]:
        counts = [str(sum(row["verified_success"] for row in rows if row["case"] == case and row["arm"] == arm)) +
                  "/" + str(plan["repeats"]) for arm in ARMS]
        lines.append(f"| {'Working repair' if case == 'good' else 'Deliberately wrong repair'} | {' | '.join(counts)} |")
    lines.extend(["", "## Every attempt", ""])
    for row in rows:
        attempts = row.get("verification_attempts", [])
        meaning = ("The fix passed every required check; I still need to review it" if row["verified_success"] else
                   "I need to inspect the evidence" if row["unconfirmed_completion"] else
                   "The deliberately bad fix failed its check, as expected" if attempts and attempts[-1]["outcome"] == "repair_failed" else
                   "Stopped before the fix was checked" if row["outcome"] == "budget_exhausted" else "Didn't finish")
        lines.append(f"- {row['case']}, repeat {row['repeat']}, {row['arm']}: {meaning} ({row['outcome']}).")
        lines.append(f"  Saved result: {row.get('receipt', 'not yet recorded')}. Time: {row.get('duration_seconds', 'unknown')} seconds.")
    lines.extend(["", "## What I can take from this", "",
                  "Automatic checking can finish a working fix under this prepared condition. The bad fix must still fail.",
                  "An unchecked fix is unfinished. That doesn't tell me whether the fix itself is wrong.",
                  "I won't adopt a change on the strength of missing evidence or an incorrect success claim.",
                  "Evidence still needs attention: " + ("yes" if result["promotion_blocked"] else "no") + ". My review is still pending.",
                  "This is a small test of one bug. Repeating it checks consistency; it doesn't establish general AI repair ability.",
                  "I still need real attempts from plain Pi and Pi with the harness, a human-plus-script baseline, and measurements of my corrections and review time.",
                  "Repair-model calls, tokens and model cost here: zero. Execution billing and my review time: unknown.",
                  "Running the checks took extra computer time. I haven't shown that it saved my time.",
                  "Keep plan.json, progress.json, comparison.json and the linked evidence together. The JSON keeps every attempt and the detailed figures.",
                  "Next I need to review the working and failed examples before choosing another change to test.",
                  "[Shared glossary and file guide (#111)](https://github.com/adamcbrewer/mapbox-tui/issues/111).", ""])
    return "\n".join(lines)


def run_comparison(directory):
    plan = load_plan(directory)
    started = time.monotonic()
    progress = {"plan_sha256": fingerprint(plan), "started_at": now(),
                "cells": [dict(cell, status="pending") for cell in plan["schedule"]]}
    with (directory / "progress.json").open("x") as output:
        output.write(json.dumps(progress, indent=2) + "\n")
    try:
        for cell in progress["cells"]:
            load_plan(directory)
            print(f"Starting {cell['case']} repeat {cell['repeat']}: {cell['arm']}", flush=True)
            run_cell(plan, cell, progress, directory)
            load_plan(directory)
            summarize(directory)
            print(f"Finished: {cell.get('receipt')}", flush=True)
    finally:
        progress.update(ended_at=now(), duration_seconds=round(time.monotonic() - started, 4))
        durable_json(directory / "progress.json", progress)
        summarize(directory)


def main():
    parser = argparse.ArgumentParser(description="Save a plan, then compare stopping at the request limit with automatic checking. No model calls.")
    action = parser.add_mutually_exclusive_group(required=True)
    action.add_argument("--prepare", action="store_true")
    action.add_argument("--run", type=Path)
    action.add_argument("--report", type=Path)
    parser.add_argument("--image")
    parser.add_argument("--repeats", type=int, choices=(2, 3), default=2)
    args = parser.parse_args()
    try:
        if args.prepare:
            if not args.image:
                parser.error("--prepare needs the existing immutable --image")
            print(prepare(args.image, args.repeats))
        elif args.run:
            run_comparison(args.run)
            print(args.run / "comparison.md")
            result = json.loads((args.run / "comparison.json").read_text())
        else:
            result = summarize(args.report)
            print(args.report / "comparison.md")
        return 0 if args.prepare else int(result["promotion_blocked"])
    except (Stop, OSError, ValueError, KeyError, TypeError) as error:
        print(error.reason if isinstance(error, Stop) else str(error))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
