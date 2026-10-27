import argparse
import copy
import json
from pathlib import Path
import re
import signal

import baseline
from recovery import durable_json
from run import HERE, REPORTS, Stop, digest, now
from verify import fingerprint
import workflow

ORDER = ((1, "plain"), (1, "managed"), (2, "managed"), (2, "plain"))


def sources():
    return {**workflow.sources(), "workflow_compare.py": digest(Path(__file__).read_bytes())}


def prepare(suite, image, config, case_id=None):
    path = workflow.prepare(suite, image, config, "plain", case_id, REPORTS / "workflow-pairs/experiments")
    common = workflow.load_plan(path)
    cells = []
    for position, (pair, mode) in enumerate(ORDER, 1):
        cell = {"position": position, "pair": pair, "mode": mode, "experiment_id": path.name}
        child = copy.deepcopy(common)
        child.update(mode=mode, comparison=cell)
        directory = path / "cells" / str(position)
        directory.mkdir(parents=True)
        durable_json(directory / "plan.json", {"definition": child, "sha256": fingerprint(child)})
        cells.append({**cell, "plan_sha256": fingerprint(child)})
    plan = {"kind": "native-workflow-pairs-v1", "created_at": now(), "common": common, "cells": cells,
            "sources": sources(), "maximum_model_requests": 4 * common["config"]["maxModelRequests"],
            "primary_metric": "More exact final repairs passing all checks within the original limits without delivered operator corrections",
            "adoption_guard": "No incorrect success claim; keep plain Pi if managed doesn't improve checked-repair count. Human approval stays pending.",
            "human_participation": "Only explicit operator events count; absent attention/review time stays unknown"}
    durable_json(path / "plan.json", {"definition": plan, "sha256": fingerprint(plan)})
    source = path / "executed-source"
    for name, expected in plan["sources"].items():
        raw = (HERE / name).read_bytes()
        if digest(raw) != expected:
            raise Stop("invalid_input", "The implementation changed during preparation")
        target = source / (name + ".txt")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(raw)
    (path / "plan.md").write_text("# Does automatic checking and feedback improve repairs?\n\n"
        "I saved four fresh runs before launch: plain, managed, managed, plain. Both start from identical broken copies, "
        "using the same initial prompt, normal tools, model/settings, image, limits and independent checker. "
        "Only managed Pi can receive one automatic retry prompt after a failed check. Context and the shared counters are retained. "
        "No private test inputs or known fix are sent back. Operator corrections are possible in either mode, and are recorded.\n\n"
        "The main result is final repairs passing every check within the limits without delivered operator corrections. "
        "I also record first-checked-candidate success, failed attempts, requests, tools, tokens and elapsed time. "
        "Keep the simpler plain workflow if managed doesn't improve that result. Incorrect success claims block adoption.\n\n"
        f"Task: {common['case']['id']}; partition: {common['case']['partition']}. At most {plan['maximum_model_requests']} model requests. "
        f"Each run keeps {common['budgets']['tool_calls']} tools, two candidate checks, ten minutes overall and two minutes per provider request. "
        "No replacement attempts or automatic transport retries. A blocked, cancelled or invalid run stops later cells.\n\n"
        "A run without operator feedback isn't a measured human-supervised baseline. Attention/review time and billing stay unknown "
        "without explicit evidence. These are synthetic tasks and a small sample, not a broad repair benchmark. "
        "My claim review and stage approval remain pending.\n")
    return path


def load_plan(path, execution=True):
    saved = json.loads((path / "plan.json").read_text())
    plan = saved["definition"]
    if (fingerprint(plan) != saved["sha256"] or plan["kind"] != "native-workflow-pairs-v1" or
            execution and plan["sources"] != sources() or
            [(c["pair"], c["mode"]) for c in plan["cells"]] != list(ORDER) or
            [c["position"] for c in plan["cells"]] != [1, 2, 3, 4] or
            any(c["experiment_id"] != path.name for c in plan["cells"])):
        raise Stop("invalid_input", "The paired workflow plan or implementation changed")
    for cell in plan["cells"]:
        directory = path / "cells" / str(cell["position"])
        child = workflow.load_plan(directory, execution)
        expected = copy.deepcopy(plan["common"])
        expected.update(mode=cell["mode"], comparison={k: cell[k] for k in ("position", "pair", "mode", "experiment_id")})
        if child != expected or fingerprint(child) != cell["plan_sha256"]:
            raise Stop("invalid_input", "A paired workflow differs from its common settings")
    return plan


def collect(path, cell):
    directory = path / "cells" / str(cell["position"])
    if not (directory / "attempt.json").exists():
        return {**cell, "status": "not_started"}
    identity = json.loads((directory / "attempt.json").read_text())["run_id"]
    if not re.fullmatch(r"[0-9a-f]{32}", identity):
        raise Stop("invalid_input", "Invalid workflow run identity")
    run = REPORTS / "workflows/runs" / identity
    raw = (run / "result.json").read_bytes()
    initial = json.loads(raw)
    if not initial.get("ended_at"):
        return {**cell, "status": "incomplete", "run_id": identity}
    result = workflow.validated_result(run)
    if (result.get("comparison") != {k: cell[k] for k in ("position", "pair", "mode", "experiment_id")} or
            result["plan_sha256"] != cell["plan_sha256"] or result["candidate_source"] != "AI" or
            result["model"] != result["config"]["model"] or result["provider"] != result["config"]["provider"]):
        raise Stop("invalid_input", "The workflow result doesn't belong to its pair")
    progress = json.loads((path / "progress.json").read_text()) if (path / "progress.json").exists() else {"cells": []}
    finished = next((r for r in progress["cells"] if r["position"] == cell["position"] and r["status"] == "finished"), None)
    if finished and (finished["run_id"] != identity or finished["receipt_sha256"] != digest(raw)):
        raise Stop("invalid_input", "The original workflow receipt changed")
    attempts = result.get("verification_attempts", [])
    corrections = sum(c["origin"] == "operator" for c in result.get("continuations", []))
    verified = result.get("independent_checks_passed") is True and result["outcome"] == "native_verification_passed"
    return {**cell, "status": "finished" if finished and result.get("ended_at") else "incomplete", "run_id": identity,
            "receipt_sha256": digest(raw), "outcome": result["outcome"], "agent_outcome": result.get("agent_outcome"),
            "verified": verified, "without_operator_correction": verified and corrections == 0,
            "first_candidate_passed": bool(attempts) and attempts[0]["outcome"] == "repository_verification_passed",
            "first_repair_passed": verified and result.get("first_repair_passed") is True,
            "attempts": len(attempts), "operator_corrections_delivered": corrections,
            "operator": operator_summary(run), "requests": result["model_requests"], "tools": result.get("tool_calls", 0),
            "failed_tools": sum(bool(t["error"]) for t in result.get("tool_results", [])),
            "duration_seconds": result["duration_seconds"], "usage": result["usage_summary"],
            "containers_created": len(result["containers"]) + sum(a["containers_created"] for a in attempts),
            "containers_removed": sum(c["removed"] for c in result["containers"]) + sum(a["containers_removed"] for a in attempts)}


def operator_summary(run):
    try:
        return workflow.operator_log.summary(run)
    except (OSError, ValueError, KeyError, TypeError, Stop):
        return {"attention_seconds": None, "timing_complete": False}


def summarize(path):
    plan = load_plan(path, execution=False)
    rows = [collect(path, cell) for cell in plan["cells"]]
    complete = all(r["status"] == "finished" for r in rows)
    totals = {}
    for mode in ("plain", "managed"):
        selected = [r for r in rows if r["mode"] == mode and r["status"] == "finished"]
        totals[mode] = {"runs": len(selected), "verified": sum(r["verified"] for r in selected),
                        "without_operator_correction": sum(r["without_operator_correction"] for r in selected),
                        "first_candidate_passed": sum(r["first_candidate_passed"] for r in selected),
                        "first_repair_passed": sum(r.get("first_repair_passed", False) for r in selected),
                        "requests": sum(r["requests"] for r in selected), "tools": sum(r["tools"] for r in selected),
                        "duration_seconds": round(sum(r["duration_seconds"] for r in selected), 4),
                        "tokens": sum(r["usage"]["totals"]["totalTokens"] for r in selected) if selected and all(r["usage"].get("complete") for r in selected) else None}
    review = json.loads((path / "claim-review.json").read_text()) if (path / "claim-review.json").exists() else {}
    claims_complete = complete and all(any(item.get("run_id") == r["run_id"] and item.get("receipt_sha256") == r["receipt_sha256"]
                                          and type(item.get("incorrect_success_claim")) is bool for item in review.get("runs", [])) for r in rows)
    claims_wrong = claims_complete and any(item["incorrect_success_claim"] for item in review["runs"])
    improved = complete and totals["managed"]["without_operator_correction"] > totals["plain"]["without_operator_correction"]
    recommendation = ("Pending complete runs and claim review" if not complete or not claims_complete else
                      "Don't adopt: an incorrect success claim needs investigation" if claims_wrong else
                      "Managed improved this small comparison; my approval remains pending" if improved else
                      "Keep plain Pi; managed didn't improve checked-repair count in this sample")
    result = {"complete": complete, "primary_improved": improved, "claim_review_complete": claims_complete,
              "incorrect_success_claim_found": claims_wrong, "recommendation": recommendation, "totals": totals, "rows": rows,
              "partition": plan["common"]["case"]["partition"], "task": plan["common"]["case"]["id"],
              "model_cost_usd": None, "execution_cost_usd": None, "human_review": "pending"}
    durable_json(path / "comparison.json", result)
    lines = ["# Did the managed loop help?", "", "I compared four fresh Pi repairs: plain, managed, managed, plain. "
             "The initial task, prompt, normal tools, model, image, limits and final checker stayed fixed. Managed Pi could receive "
             "one automatic retry prompt after a failed check. These are synthetic tasks, not evidence of general real-world repair skill.", "",
             "| Run | Mode | Checked fix | Candidate checks | Operator corrections delivered | Model requests | Tools | Tokens | Seconds |",
             "| --- | --- | --- | --- | --- | --- | --- | --- | --- |"]
    for r in rows:
        if r["status"] != "finished":
            lines.append(f"| {r['position']} | {r['mode']} | {r['status']} | | | | | | |")
            continue
        tokens = r["usage"].get("totals", {}).get("totalTokens", "unknown") if r["usage"].get("complete") else "unknown"
        lines.append(f"| {r['position']} | {r['mode']} | {'Passed' if r['verified'] else 'Not established'} | {r['attempts']} | "
                     f"{r['operator_corrections_delivered']} | {r['requests']} | {r['tools']} | {tokens} | {r['duration_seconds']} |")
    lines += ["", recommendation + ".", "", "Every attempt stays in the results, including failures and unfinished runs. "
              "First-checked-candidate success and final success are separate in comparison.json. Short failed runs aren't faster repairs.", "",
              "Zero delivered corrections doesn't establish zero attention. These runs aren't a measured human-supervised baseline "
              "unless explicit operator participation was recorded. My attention/review time and billing stay unknown without evidence. "
              "A checked fix and my approval are separate.", "",
              "The plan, conversations, operator records, request reservations, candidate attempts and independent receipts stay local. "
              "Use workflow.py --report with a run folder to validate its evidence without inference. "
              "The [shared file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111) explains the wider workflow.", ""]
    (path / "comparison.md").write_text("\n".join(lines))
    return result


def execute(path):
    plan = load_plan(path)
    baseline.claim(path, "experiment-" + path.name)
    if any((path / "cells" / str(c["position"]) / "attempt.json").exists() for c in plan["cells"]):
        raise Stop("invalid_input", "A workflow cell already started; there are no replacement attempts")
    progress = {"cells": []}
    def cancel(signum, frame):
        raise KeyboardInterrupt
    handlers = {n: signal.signal(n, cancel) for n in (signal.SIGINT, signal.SIGTERM)}
    try:
        for cell in plan["cells"]:
            load_plan(path)
            record = {**cell, "status": "started"}
            progress["cells"].append(record)
            durable_json(path / "progress.json", progress)
            print(f"Starting {cell['position']}: {cell['mode']}", flush=True)
            directory, result = workflow.execute(path / "cells" / str(cell["position"]))
            record.update(status="finished", run_id=directory.name, receipt_sha256=digest((directory / "result.json").read_bytes()))
            durable_json(path / "progress.json", progress)
            summarize(path)
            print(result["outcome"] + ": " + str(directory / "workflow.md"), flush=True)
            stop = next((result.get(key) for key in ("outcome", "agent_outcome")
                         if result.get(key) in ("cancelled", "blocked", "invalid_input")), None)
            if stop:
                raise Stop(stop, "The paired experiment stopped; later cells weren't started")
    except KeyboardInterrupt as error:
        raise Stop("cancelled", "The paired experiment stopped") from error
    finally:
        try:
            summarize(path)
        finally:
            for n, handler in handlers.items():
                signal.signal(n, handler)
    return summarize(path)


def main():
    parser = argparse.ArgumentParser(description="Save, run or inspect a fixed four-run Pi workflow comparison.")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--prepare", action="store_true")
    mode.add_argument("--run", type=Path)
    mode.add_argument("--report", type=Path)
    parser.add_argument("--suite", type=Path)
    parser.add_argument("--image")
    parser.add_argument("--config", type=Path)
    parser.add_argument("--case")
    args = parser.parse_args()
    try:
        if args.prepare:
            if not all((args.suite, args.image, args.config)):
                parser.error("--prepare needs --suite, --image and --config")
            print(prepare(args.suite, args.image, args.config, args.case))
            return 0
        path = args.run or args.report
        result = execute(path) if args.run else summarize(path)
        print(path / "comparison.md")
        return 0 if result["complete"] else 1
    except (Stop, OSError, ValueError, KeyError, TypeError) as error:
        print(error.reason if isinstance(error, Stop) else str(error))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
