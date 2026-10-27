import argparse
import base64
import copy
import json
from pathlib import Path
import re
import shutil
import signal
import tempfile

import baseline
from native_sandbox import NativeRun, NativeSandbox
from recovery import durable_json
from run import HERE, REPORTS, Stop, digest, now
from sandbox import preflight
from verify import fingerprint, receipt_matches

ORDER = ((1, "baseline"), (1, "guidance"), (2, "guidance"), (2, "baseline"))
GUIDANCE = ("\n\nContainer execution context: All coding tools operate inside the disposable /workspace container, "
            "not the host agent environment. Go 1.27.1 is already on PATH as go; make, gofmt, python3, bash, find and grep "
            "are available. The repository's mise prefix applies to host execution only: inside this container run go and "
            "make directly. mise and rg are not installed; use find and grep for searching. The project is an archive "
            "without .git metadata, so git status and git diff won't work here. Inspect files with the normal coding tools. "
            "Keep the same source scope and test requirements; don't install anything or change tests.")


def sources():
    return {**baseline.sources(), "paired.py": digest(Path(__file__).read_bytes())}


def probe(common):
    run = NativeRun(REPORTS / "paired-preflight", common["image"])
    try:
        preflight(run)
        run.prepare()
        with tempfile.TemporaryDirectory(prefix="mapbox-calibration-guidance-") as temporary:
            shutil.copytree(Path(common["suite"]) / "cases" / common["case"]["id"] / "agent", temporary, dirs_exist_ok=True)
            sandbox = NativeSandbox(run, Path(temporary))
            try:
                sandbox.start()
                command = ('for tool in go make gofmt python3 bash find grep; do command -v "$tool" >/dev/null || exit 1; done; '
                           'go version && test ! -e /workspace/.git && ! command -v mise && ! command -v rg')
                result = sandbox.request({"op": "bash", "command": command, "timeout": 5})
                if not result["ok"] or result["value"]["exitCode"] or not base64.b64decode(result["value"]["output"]).startswith(b"go version go1.27.1 "):
                    raise Stop("blocked", "The guidance doesn't match the prepared container tools")
            finally:
                sandbox.close()
        run.result["outcome"] = "guidance_tools_confirmed"
    finally:
        run.event("guidance_probe_finished")
    return {"run_id": run.path.name, "receipt_sha256": digest((run.path / "result.json").read_bytes())}


def prepare(suite, image, config):
    path = baseline.prepare(suite, image, config, output_root=REPORTS / "native-pairs/experiments")
    common = baseline.load_plan(path)
    observed = probe(common)
    cells = []
    for position, (pair, arm) in enumerate(ORDER, 1):
        cell = {"position": position, "pair": pair, "arm": arm, "experiment_id": path.name}
        child = copy.deepcopy(common)
        child["comparison"] = cell
        if arm == "guidance":
            child["context"]["prompt"] += GUIDANCE
        directory = path / "cells" / str(position)
        directory.mkdir(parents=True)
        durable_json(directory / "plan.json", {"definition": child, "sha256": fingerprint(child)})
        (directory / "plan.md").write_text(f"# Paired run {position}: {arm}\n\n"
            "I saved this run as part of a four-run comparison. It uses the common task, model, tools, checker and limits. "
            "Only the guidance version adds the saved container-tooling paragraph. It starts once; interrupted attempts stay spent.\n")
        cells.append({**cell, "plan_sha256": fingerprint(child)})
    plan = {"kind": "container-guidance-pairs-v1", "created_at": now(), "common": common, "guidance": GUIDANCE,
            "primary_metric": "Fewer failed Pi tool calls in each pair, while every repair passes all independent checks",
            "guardrail": "All four repair conversations finish and all four exact patches pass every required check. My claim review and adoption decision remain pending.",
            "cells": cells, "sources": sources(), "probe": observed, "pilot_excluded": True,
            "maximum_model_requests": 4 * common["config"]["maxModelRequests"]}
    durable_json(path / "plan.json", {"definition": plan, "sha256": fingerprint(plan)})
    (path / "plan.md").write_text("# Does clearer container guidance reduce wasted calls?\n\n"
        "The pilot found that Pi tried host tools inside the container. I want to test one extra paragraph explaining the available tools. "
        "It gives no repair or hidden test hints. The original prompt and repository instructions stay intact.\n\n"
        "I saved four fresh runs before inference: baseline, guidance, guidance, baseline. These form two pairs in reversed order. "
        "The pilot is excluded. Both versions use the same task copy, standard Pi system prompt, normal tools, model, reasoning, "
        "image, limits and protected checker. No feedback or extra repair attempt is added.\n\n"
        "The main measure is failed Pi tool calls. A useful result needs fewer failures in both pairs, with every conversation finishing "
        "and every exact fix passing all required checks. I also keep tokens, requests, total tools and elapsed time. Missing usage, "
        "billing and my review time stay unknown. I still need to review claims and decide whether to adopt the change.\n\n"
        f"Each run allows {common['config']['maxModelRequests']} model requests, twelve tools and ten minutes including grading. "
        f"The whole experiment allows at most {plan['maximum_model_requests']} model requests. There are no automatic retries or replacements. "
        "A cancelled or blocked run stops the remaining experiment. The held-out task stays unused.\n\n"
        "## The only added text\n\n" + GUIDANCE.strip() + "\n")
    return path


def saved_child(directory):
    saved = json.loads((directory / "plan.json").read_text())
    if fingerprint(saved["definition"]) != saved["sha256"]:
        raise Stop("invalid_input", "A saved paired cell changed")
    return saved["definition"]


def load_plan(path, execution=True):
    saved = json.loads((path / "plan.json").read_text())
    plan = saved["definition"]
    if (fingerprint(plan) != saved["sha256"] or plan["kind"] != "container-guidance-pairs-v1" or
            execution and (plan["sources"] != sources() or plan["guidance"] != GUIDANCE) or
            [c["position"] for c in plan["cells"]] != [1, 2, 3, 4] or
            any(c["experiment_id"] != path.name for c in plan["cells"]) or
            [(c["pair"], c["arm"]) for c in plan["cells"]] != list(ORDER)):
        raise Stop("invalid_input", "The paired plan or implementation changed; no model request was sent")
    for cell in plan["cells"]:
        directory = path / "cells" / str(cell["position"])
        child = baseline.load_plan(directory) if execution else saved_child(directory)
        expected = copy.deepcopy(plan["common"])
        expected["comparison"] = {key: cell[key] for key in ("position", "pair", "arm", "experiment_id")}
        if cell["arm"] == "guidance":
            expected["context"]["prompt"] += plan["guidance"]
        if child != expected or fingerprint(child) != cell["plan_sha256"]:
            raise Stop("invalid_input", "A paired run differs by more than the saved guidance")
    return plan


def collect(path, cell):
    directory = path / "cells" / str(cell["position"])
    if not (directory / "attempt.json").exists():
        return {**cell, "status": "not_started"}
    identity = json.loads((directory / "attempt.json").read_text())["run_id"]
    if not re.fullmatch(r"[0-9a-f]{32}", identity):
        raise Stop("invalid_input", "A paired run has an invalid saved identity")
    run = REPORTS / "native-baseline/runs" / identity
    raw = (run / "result.json").read_bytes()
    result = json.loads(raw)
    if (result["run_id"] != identity or result["plan_sha256"] != cell["plan_sha256"] or
            result.get("comparison") != {key: cell[key] for key in ("position", "pair", "arm", "experiment_id")}):
        raise Stop("invalid_input", "The run doesn't belong to its saved pair")
    progress = json.loads((path / "progress.json").read_text()) if (path / "progress.json").exists() else {"cells": []}
    finished = next((item for item in progress["cells"] if item["position"] == cell["position"] and item["status"] == "finished"), None)
    if finished and (finished["run_id"] != identity or finished["receipt_sha256"] != digest(raw)):
        raise Stop("invalid_input", "The saved paired receipt changed after it finished")
    row = {**cell, "status": "finished" if result.get("ended_at") and finished else "incomplete", "run_id": identity,
           "receipt_sha256": digest(raw), "outcome": result["outcome"], "agent_outcome": result.get("agent_outcome"),
           "verified": False, "failed_tools": sum(bool(t["error"]) for t in result.get("tool_results", [])),
           "tools": result.get("tool_calls", 0), "requests": result["model_requests"],
           "duration_seconds": result.get("duration_seconds"), "usage": result.get("usage_summary"),
           "containers_removed": sum(c["removed"] for c in result["containers"]), "containers_created": len(result["containers"])}
    child_plan = saved_child(directory)
    if (result["workflow"] != "plain-pi-live-baseline" or result["candidate_source"] != "AI" or
            any(result[key] != child_plan["config"][key] for key in ("model", "provider", "auth")) or
            result["image"] != child_plan["image"] or result["evaluator"] != child_plan["evaluator"] or
            result["budgets"] != child_plan["budgets"]):
        raise Stop("invalid_input", "The observed model or limits differ from the paired plan")
    if result["model_requests"]:
        if not (run / "context.json").exists():
            raise Stop("invalid_input", "A dispatched paired run is missing its actual context")
        reservations = json.loads((run / "reservations.json").read_text())
        if (len(reservations) != result["model_requests"] or len(reservations) > child_plan["config"]["maxModelRequests"] or
                any(item["request"] != number or any(item[key] != child_plan["config"][key] for key in ("provider", "model"))
                    for number, item in enumerate(reservations, 1)) or
                len(result["dispatches"]) != len(set(result["dispatches"])) or
                any(type(number) is not int or not 1 <= number <= len(reservations) for number in result["dispatches"])):
            raise Stop("invalid_input", "The paired model reservations or dispatches don't match")
    try:
        row["usage"] = baseline.token_summary(result["provider_usage"], len(result["dispatches"]))
    except (ValueError, TypeError, KeyError):
        row["usage"] = {"complete": False}
    if (run / "context.json").exists():
        context = json.loads((run / "context.json").read_text())
        if ({key: context[key] for key in baseline.CONTEXT} != child_plan["context"] or
                context["permissions"] != {"network": True, "childProcess": False}):
            raise Stop("invalid_input", "The actual paired context changed")
    if result.get("independent_checks_passed"):
        record = result["verification"]
        child = REPORTS / "verification/runs" / record["run_id"]
        content = (child / "result.json").read_bytes()
        verification = json.loads(content)
        if (digest(content) != record["receipt_sha256"] or not receipt_matches(verification, child) or
                not result["dispatches"] or
                verification["outcome"] != "repository_verification_passed" or result["outcome"] != "native_verification_passed" or
                result["duration_seconds"] > child_plan["budgets"]["deadline_seconds"] or
                verification["parent_run_id"] != identity or
                any(verification[key] != result[key] for key in ("image", "base_commit", "task_sha256", "patch_sha256", "evaluator_sha256")) or
                digest((run / "candidate.patch").read_bytes()) != verification["patch_sha256"] or
                digest((run / "candidate-source.bin").read_bytes()) != result["snapshot_source_sha256"] or
                verification["trees"]["candidate"]["files"]["go/import.go"] != result["snapshot_source_sha256"] or
                not all(c["removed"] for r in (result, verification) for c in r["containers"])):
            raise Stop("invalid_input", "A passing paired result doesn't match its independent evidence")
        row["verified"] = True
        row["containers_removed"] += len(verification["containers"])
        row["containers_created"] += len(verification["containers"])
    elif result.get("verification"):
        record = result["verification"]
        row["containers_removed"] += record["containers_removed"]
        row["containers_created"] += record["containers_created"]
    return row


def summarize(path):
    plan = load_plan(path, execution=False)
    rows = [collect(path, cell) for cell in plan["cells"]]
    complete = all(row["status"] == "finished" for row in rows)
    guarded = complete and all(row["verified"] and row["agent_outcome"] == "finished" for row in rows)
    deltas = [next(r["failed_tools"] for r in rows if r["pair"] == pair and r["arm"] == "baseline") -
              next(r["failed_tools"] for r in rows if r["pair"] == pair and r["arm"] == "guidance") for pair in (1, 2)] if complete else []
    consistent = guarded and all(delta > 0 for delta in deltas)
    result = {"experiment": path.name, "rows": rows, "complete": complete, "guardrail_met": guarded,
              "primary_improved_in_both_pairs": consistent, "failed_call_reductions": deltas,
              "adoption": "awaiting claim review and Adam's decision", "model_cost_usd": None,
              "execution_cost_usd": None, "human_review_time_seconds": None}
    durable_json(path / "comparison.json", result)
    lines = ["# Does clearer container guidance help?", "",
             "I compared the original prompt with one extra paragraph about the tools available inside the container. "
             f"Real runs used {plan['common']['config']['model']} with normal Pi tools. The pilot isn't included, and the held-out task wasn't used.", "",
             "The main measure was failed Pi tool calls. Every repair still had to pass the same independent checks. "
             "The order was baseline, guidance, guidance, baseline. Everything else stayed fixed in the saved plan.", "",
             "| Run | Pair | Version | Checked fix | Failed tools | All tools | Requests | Reported tokens | Seconds |",
             "| --- | --- | --- | --- | --- | --- | --- | --- | --- |"]
    for row in rows:
        usage = row.get("usage") or {}
        tokens = usage.get("totals", {}).get("totalTokens", "unknown") if usage.get("complete") else "unknown"
        lines.append(f"| {row['position']} | {row['pair']} | {row['arm']} | " +
                     ("Passed" if row.get("verified") else "Failed" if row.get("outcome") == "repair_failed" else "Not confirmed") +
                     f" | {row.get('failed_tools', 'not run')} | {row.get('tools', 'not run')} | {row.get('requests', 'not run')} | {tokens} | {row.get('duration_seconds', 'not run')} |")
    finding = ("The guidance version had fewer failed calls in both pairs, and all four repairs passed. This is a candidate to review on this task." if consistent else
               "The comparison didn't show a consistent reduction with every repair passing. I don't have enough evidence to adopt the change." if complete else
               "The comparison is unfinished. Missing runs don't count as successful, and I won't fill them with replacement attempts.")
    lines += ["", finding, "", "Failed-call reductions by pair: " + str(deltas) + ".", "",
              "I still need to inspect the model's testing claims and record my adoption decision. Two pairs on one synthetic bug "
              "don't establish real-world repair skill or time saved for me. Model billing, execution billing and my review time are unknown.", "",
              "Read [the saved protocol](plan.md). Each numbered cells folder keeps its one-use plan and run identity. "
              "The run reports, conversations, request reservations and independent checks stay local. "
              "The [shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111) explains these files.", ""]
    (path / "comparison.md").write_text("\n".join(lines))
    return result


def execute(path):
    plan = load_plan(path)
    baseline.claim(path, "paired-" + path.name)
    if any((path / "cells" / str(cell["position"]) / "attempt.json").exists() for cell in plan["cells"]):
        raise Stop("invalid_input", "A paired cell already started outside the saved order")
    progress = {"started_at": now(), "cells": []}
    def cancel(signum, frame):
        raise KeyboardInterrupt
    handlers = {number: signal.signal(number, cancel) for number in (signal.SIGINT, signal.SIGTERM)}
    try:
        for cell in plan["cells"]:
            load_plan(path)
            row = {**cell, "status": "started", "at": now()}
            progress["cells"].append(row)
            durable_json(path / "progress.json", progress)
            directory, result = baseline.execute(path / "cells" / str(cell["position"]))
            row.update(status="finished", run_id=directory.name, outcome=result["outcome"],
                       receipt_sha256=digest((directory / "result.json").read_bytes()))
            durable_json(path / "progress.json", progress)
            summarize(path)
            stopped = (result["outcome"] if result["outcome"] in ("cancelled", "blocked", "invalid_input", "incomplete") else
                       result.get("agent_outcome") if result.get("agent_outcome") in ("cancelled", "blocked", "invalid_input") else None)
            if stopped:
                raise Stop(stopped, "The paired experiment stopped; later cells weren't started")
    except KeyboardInterrupt as error:
        raise Stop("cancelled", "The paired experiment was stopped; later cells weren't started") from error
    finally:
        try:
            summarize(path)
        finally:
            for number, handler in handlers.items():
                signal.signal(number, handler)
    return summarize(path)


def main():
    parser = argparse.ArgumentParser(description="Save, run or inspect two live pairs testing container-tooling guidance.")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--prepare", action="store_true")
    mode.add_argument("--run", type=Path)
    mode.add_argument("--report", type=Path)
    parser.add_argument("--suite", type=Path)
    parser.add_argument("--image")
    parser.add_argument("--config", type=Path)
    args = parser.parse_args()
    try:
        if args.prepare:
            if not all((args.suite, args.image, args.config)):
                parser.error("--prepare needs --suite, --image and --config")
            print(prepare(args.suite, args.image, args.config))
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
