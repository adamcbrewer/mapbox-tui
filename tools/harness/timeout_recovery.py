import argparse
import base64
import copy
import json
from pathlib import Path
import re
import shutil
import signal
import tempfile
import time

import baseline
from native_run import session
from native_sandbox import NativeRun, NativeSandbox
from native_worker import TIMEOUT_ERROR
from recovery import durable_json
from run import REPORTS, Stop, digest, now
from sandbox import preflight
from verify import fingerprint

MESSAGES = {"generic": "The command needs a valid timeout", "clear": TIMEOUT_ERROR}
ORDER = ((1, "generic"), (1, "clear"), (2, "clear"), (2, "generic"))
TASK = {"id": "supplied-timeout-recovery-v1", "synthetic": True, "allowed_files": [],
        "supplied_call": {"name": "bash", "args": {"command": "pwd", "timeout": 120000}}}


def prompt(version):
    return ("This is a tool-request recovery test. Do not edit project files. A supplied setup step made this Bash request: " +
            json.dumps(TASK["supplied_call"]["args"], sort_keys=True) + ". It was refused before execution with this tool error:\n" +
            MESSAGES[version] + "\nRun exactly pwd with an acceptable timeout, or omit the timeout, then report the working directory. "
            "No other commands are needed. The supplied failure was a test fixture, not your own earlier action. "
            "It counts once against the shared tool-call limit. You choose the correction.")


def sources():
    return {**baseline.sources(), "timeout_recovery.py": digest(Path(__file__).read_bytes())}


def prepare(suite, image, config):
    path = baseline.prepare(suite, image, config, output_root=REPORTS / "timeout-recovery/experiments")
    common = baseline.load_plan(path)
    common["context"] = baseline.preview(TASK, prompt("clear"))
    common["evaluator"] = {"scope": TASK["id"], "command": "pwd", "output": "/workspace\n", "project_unchanged": True}
    cells = []
    for position, (pair, version) in enumerate(ORDER, 1):
        cell = {"position": position, "pair": pair, "version": version, "experiment_id": path.name}
        child = copy.deepcopy(common)
        child.update(kind="timeout-recovery-run-v1", feedback=version, comparison=cell)
        child["context"]["prompt"] = prompt(version)
        directory = path / "cells" / str(position)
        directory.mkdir(parents=True)
        durable_json(directory / "plan.json", {"definition": child, "sha256": fingerprint(child)})
        cells.append({**cell, "plan_sha256": fingerprint(child)})
    plan = {"kind": "timeout-recovery-pairs-v1", "created_at": now(), "common": common, "cells": cells,
            "messages": MESSAGES, "sources": sources(), "maximum_model_requests": 4 * common["config"]["maxModelRequests"],
            "primary_metric": "Fewer repeated invalid timeout calls after the common supplied failure in both pairs",
            "guardrail": "Every run executes pwd with a valid timeout, keeps the project unchanged and finishes within the original limits",
            "held_out_used": False}
    durable_json(path / "plan.json", {"definition": plan, "sha256": fingerprint(plan)})
    (path / "plan.md").write_text("# Does a clearer timeout error help the AI recover?\n\n"
        "I saved four fresh runs: generic, clear, clear, generic. The only difference is the error returned for an invalid timeout. "
        "Both versions start with the same supplied failed pwd request. It is recorded as a fixture, not an AI action, and consumes one tool call. "
        f"{common['config']['model']} then chooses its own correction using normal Pi tools. This is a focused recovery task, not a bug-repair benchmark.\n\n"
        "The main measure is repeated invalid timeout calls after the supplied failure. A useful result needs fewer repeats in both pairs, "
        "with every run executing pwd, keeping the project unchanged and finishing. If both recover immediately, there is no measured improvement. "
        "The tool schema, task instructions, model, reasoning, image and limits stay fixed. Only the quoted feedback differs.\n\n"
        f"Each run keeps {common['config']['maxModelRequests']} model requests, twelve tool calls including the supplied failure, "
        f"two minutes per request and ten minutes overall. At most {plan['maximum_model_requests']} model requests across four runs. "
        "No replacement attempts. A blocked, cancelled or invalid run stops the remaining experiment.\n\n"
        "I check actual sandbox command logs and source snapshots, not the AI's claim of success. Missing usage and billing stay unknown. "
        "The held-out task, human baseline and my learning review remain pending.\n\n"
        "## Generic feedback\n\n" + MESSAGES["generic"] + "\n\n## Clear feedback\n\n" + MESSAGES["clear"] + "\n")
    return path


def load_plan(path, execution=True):
    saved = json.loads((path / "plan.json").read_text())
    plan = saved["definition"]
    if (fingerprint(plan) != saved["sha256"] or plan["kind"] != "timeout-recovery-pairs-v1" or
            (execution and (plan["sources"] != sources() or plan["messages"] != MESSAGES)) or
            [(c["pair"], c["version"]) for c in plan["cells"]] != list(ORDER) or
            [c["position"] for c in plan["cells"]] != [1, 2, 3, 4] or any(c["experiment_id"] != path.name for c in plan["cells"])):
        raise Stop("invalid_input", "The recovery plan or implementation changed; no model request was sent")
    for cell in plan["cells"]:
        child = read_child(path, cell)
        expected = copy.deepcopy(plan["common"])
        expected.update(kind="timeout-recovery-run-v1", feedback=cell["version"],
                        comparison={key: cell[key] for key in ("position", "pair", "version", "experiment_id")})
        expected["context"]["prompt"] = plan["common"]["context"]["prompt"].replace(plan["messages"]["clear"], plan["messages"][cell["version"]])
        if child != expected:
            raise Stop("invalid_input", "A recovery run differs by more than the saved feedback")
    if execution:
        baseline.validate_live_config(plan["common"]["config"])
        suite = Path(plan["common"]["suite"])
        manifest = baseline.load_suite(suite)
        if digest((suite / "suite.json").read_bytes()) != plan["common"]["suite_sha256"] or plan["common"]["case"] not in manifest["cases"]:
            raise Stop("invalid_input", "The recovery source fixture changed")
        baseline.validate_case(suite / "cases" / plan["common"]["case"]["id"], plan["common"]["case"])
    return plan


def read_child(path, cell):
    saved = json.loads((path / "cells" / str(cell["position"]) / "plan.json").read_text())
    if fingerprint(saved["definition"]) != saved["sha256"] or saved["sha256"] != cell["plan_sha256"]:
        raise Stop("invalid_input", "A saved recovery cell changed")
    return saved["definition"]


class RecoveryTools(baseline.LiveTools):
    def __init__(self, run, sandbox, plan):
        super().__init__(run, sandbox, plan)
        self.timeout_error = MESSAGES[plan["feedback"]]
        run.result["command_checks"] = []

    def dispatch(self, request):
        result = super().dispatch(request)
        if request.get("type") == "native_fs" and request.get("op") == "bash" and result.get("ok"):
            check = self.run.result["checks"][-1]
            record = {"check_id": check["id"], "stdout_sha256": digest((self.run.path / check["stdout"]).read_bytes()),
                      "live_call": self.calls - 1, "model_requests": self.run.result["model_requests"]}
            self.run.result["command_checks"].append(record)
            self.run.event("recovery_command_observed", record=record)
        return result


def supply_failure(run, tools, temporary, version):
    session(run, tools, {"task": TASK, "calls": [TASK["supplied_call"]]}, temporary)
    for name in ("context.json", "pi-session.json", "pi.stderr"):
        (run.path / name).rename(run.path / ("supplied-" + name))
    if tools.calls != 1 or tools.results != [{"name": "bash", "error": MESSAGES[version]}] or run.result["command_checks"]:
        raise Stop("invalid_input", "The supplied failure did not match the frozen setup")
    run.event("supplied_failure_recorded", source="prepared fixture", tool_calls=1, actual_model_calls=0)


def recovery_matches(result, directory, plan):
    try:
        if (result["snapshot_scope_valid"] is not True or result["changed_files"] or result.get("snapshot_problem") or
                not result["containers"] or not all(c["removed"] for c in result["containers"]) or
                not result["dispatches"] or not result["command_checks"] or
                any(t["name"] in ("write", "edit") for t in result["tool_results"])):
            return False
        snapshot = json.loads((directory / "project-snapshot.json").read_text())
        if snapshot["files"] != plan["case"]["agent_files_sha256"] or snapshot["invalid"]:
            return False
        for record in result["command_checks"]:
            identity = record["check_id"]
            if not re.fullmatch(r"native-(?:[1-9][0-9]*-)?op-[1-9][0-9]*", identity):
                return False
            check = next(c for c in result["checks"] if c["id"] == identity)
            args = check["command"]
            operation = json.loads(args[-1])
            raw = (directory / (identity + ".stdout")).read_bytes()
            reply = json.loads(raw)
            if (check["stdout"] != identity + ".stdout" or check["exit_code"] != 0 or
                    args[:4] != ["podman", "--remote=false", "exec", result["containers"][0]["name"]] or
                    args[4:7] != ["/usr/bin/python3", "-I", "-c"] or digest(args[7].encode()) != plan["sources"]["native_worker.py"] or
                    operation["op"] != "bash" or operation["command"] != "pwd" or type(operation["timeout"]) not in (int, float) or
                    not 0 < operation["timeout"] <= 600 or
                    digest(raw) != record["stdout_sha256"] or not reply["ok"] or reply["value"]["exitCode"] != 0 or
                    base64.b64decode(reply["value"]["output"], validate=True) != b"/workspace\n" or
                    not 1 <= record["live_call"] < plan["budgets"]["tool_calls"] or not 1 <= record["model_requests"] <= result["model_requests"]):
                return False
        return True
    except (OSError, ValueError, KeyError, TypeError, IndexError, StopIteration):
        return False


def run_cell(path, cell, parent):
    plan = read_child(path, cell)
    plan["sources"] = parent["sources"]
    run = NativeRun(REPORTS / "timeout-recovery", plan["image"])
    run.result.update(workflow="timeout-recovery", comparison=cell, plan_sha256=cell["plan_sha256"],
                      model=plan["config"]["model"], provider=plan["config"]["provider"], model_cost_usd=None,
                      model_requests=0, dispatches=[], provider_usage=[], budgets=plan["budgets"], recovered=False)
    handlers = {n: signal.signal(n, run.cancel) for n in (signal.SIGINT, signal.SIGTERM)}
    try:
        baseline.claim(path / "cells" / str(cell["position"]), run.path.name)
        preflight(run)
        run.prepare()
        if any(run.result[key] != plan[key] for key in ("runtime", "environment", "seccomp_sha256")):
            raise Stop("blocked", "The runtime differs from the saved recovery plan")
        with tempfile.TemporaryDirectory(prefix="mapbox-calibration-recovery-probe-") as project, tempfile.TemporaryDirectory(prefix="mapbox-timeout-session-") as node:
            shutil.copytree(Path(plan["suite"]) / "cases" / plan["case"]["id"] / "agent", project, dirs_exist_ok=True)
            sandbox = NativeSandbox(run, Path(project))
            try:
                sandbox.start()
                tools = RecoveryTools(run, sandbox, plan)
                supply_failure(run, tools, Path(node), cell["version"])
                try:
                    session(run, tools, {"mode": "live", "task": TASK, "prompt": plan["context"]["prompt"], "config": plan["config"],
                                        "authDir": str(Path.home() / ".pi/agent")}, Path(node))
                    run.result["agent_outcome"] = "finished"
                except Stop as error:
                    run.result.update(agent_outcome=error.outcome, reason=error.reason)
            finally:
                sandbox.close()
        run.result["recovered"] = recovery_matches(run.result, run.path, plan)
        run.result["outcome"] = "recovery_passed" if run.result["recovered"] else "recovery_failed"
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Recovery comparison stopped")
    except (OSError, ValueError, KeyError, TypeError) as error:
        run.result.update(outcome="blocked", reason=str(error))
    finally:
        try:
            run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
            try:
                run.result["usage_summary"] = baseline.token_summary(run.result["provider_usage"], len(run.result["dispatches"]))
            except (ValueError, TypeError, KeyError):
                run.result["usage_summary"] = {"complete": False}
            run.event("recovery_finished", outcome=run.result["outcome"])
        finally:
            for n, handler in handlers.items():
                signal.signal(n, handler)
    return run.path, run.result


def validate_receipt(result, directory, plan, message):
    calls = result.get("tool_results", [])
    if (result["run_id"] != directory.name or result["provider"] != plan["config"]["provider"] or
            result["model"] != plan["config"]["model"] or result["budgets"] != plan["budgets"] or
            not 0 <= result["model_requests"] <= plan["budgets"]["model_requests"] or
            not 0 <= result.get("tool_calls", 0) <= plan["budgets"]["tool_calls"]):
        raise Stop("invalid_input", "The recovery identity or budget doesn't match")
    if result["dispatches"]:
        context = json.loads((directory / "context.json").read_text())
        reservations = json.loads((directory / "reservations.json").read_text())
        if ({key: context[key] for key in baseline.CONTEXT} != plan["context"] or
                context["permissions"] != {"network": True, "childProcess": False} or
                len(reservations) != result["model_requests"] or len(set(result["dispatches"])) != len(result["dispatches"]) or
                any(item["request"] != number or any(item[key] != plan["config"][key] for key in ("model", "provider"))
                    for number, item in enumerate(reservations, 1)) or
                any(type(n) is not int or not 1 <= n <= len(reservations) for n in result["dispatches"]) or
                not calls or calls[0] != {"name": "bash", "error": message}):
            raise Stop("invalid_input", "The recovery context, requests or supplied failure changed")
    if len(calls) > result.get("tool_calls", 0):
        raise Stop("invalid_input", "The tool results exceed the recorded calls")
    try:
        return baseline.token_summary(result["provider_usage"], len(result["dispatches"]))
    except (ValueError, TypeError, KeyError):
        return {"complete": False}


def summarize(path):
    plan = load_plan(path, execution=False)
    rows = []
    progress = json.loads((path / "progress.json").read_text()) if (path / "progress.json").exists() else {"cells": []}
    for cell in plan["cells"]:
        saved = next((r for r in progress["cells"] if r["position"] == cell["position"] and r["status"] == "finished"), None)
        if not saved:
            rows.append({**cell, "status": "not_finished"})
            continue
        if not re.fullmatch(r"[0-9a-f]{32}", saved["run_id"]):
            raise Stop("invalid_input", "Invalid recovery run identity")
        directory = REPORTS / "timeout-recovery/runs" / saved["run_id"]
        raw = (directory / "result.json").read_bytes()
        result = json.loads(raw)
        child = read_child(path, cell)
        child["sources"] = plan["sources"]
        if digest(raw) != saved["receipt_sha256"] or result["comparison"] != cell or result["plan_sha256"] != cell["plan_sha256"]:
            raise Stop("invalid_input", "The recovery receipt no longer matches its cell")
        usage = validate_receipt(result, directory, child, plan["messages"][cell["version"]])
        recovered = recovery_matches(result, directory, child)
        if result["recovered"] != recovered:
            raise Stop("invalid_input", "The recovery claim doesn't match the command evidence")
        failures = sum(t["error"] == plan["messages"][cell["version"]] for t in result.get("tool_results", [])[1:])
        rows.append({**cell, "status": "finished", "run_id": saved["run_id"], "recovered": recovered,
                     "repeated_timeouts": failures, "agent_outcome": result.get("agent_outcome"), "requests": result["model_requests"],
                     "tools": result.get("tool_calls", 0), "usage": usage, "duration_seconds": result["duration_seconds"],
                     "containers_removed": sum(c["removed"] for c in result["containers"]), "containers_created": len(result["containers"])})
    complete = all(r["status"] == "finished" for r in rows)
    guarded = complete and all(r["recovered"] and r["agent_outcome"] == "finished" and
                               r["duration_seconds"] <= plan["common"]["budgets"]["deadline_seconds"] for r in rows)
    deltas = [next(r["repeated_timeouts"] for r in rows if r["pair"] == pair and r["version"] == "generic") -
              next(r["repeated_timeouts"] for r in rows if r["pair"] == pair and r["version"] == "clear") for pair in (1, 2)] if complete else []
    improved = guarded and all(delta > 0 for delta in deltas)
    result = {"complete": complete, "guardrail_met": guarded, "primary_improved": improved, "reductions": deltas, "rows": rows,
              "model_cost_usd": None, "execution_cost_usd": None, "human_review_time_seconds": None, "review": "pending"}
    durable_json(path / "comparison.json", result)
    lines = ["# Does the clear timeout error help recovery?", "",
             f"I supplied the same invalid pwd request to both versions, then let real {plan['common']['config']['model']} runs choose a correction. "
             "Only the error text changed. This is a focused recovery test; no code repair or held-out task was tested.", "",
             "| Run | Pair | Error | Recovered | Repeated bad timeouts | Requests | Tools, including supplied failure | Tokens | Seconds |",
             "| --- | --- | --- | --- | --- | --- | --- | --- | --- |"]
    for r in rows:
        usage = r.get("usage", {})
        tokens = usage.get("totals", {}).get("totalTokens", "unknown") if usage.get("complete") else "unknown"
        lines.append(f"| {r['position']} | {r['pair']} | {r['version']} | " + ("Yes" if r.get("recovered") else "Not confirmed") +
                     f" | {r.get('repeated_timeouts', 'not finished')} | {r.get('requests', 'not finished')} | {r.get('tools', 'not finished')} | {tokens} | {r.get('duration_seconds', 'not finished')} |")
    lines += ["", "The clear feedback reduced repeated mistakes in both pairs." if improved else
              "The comparison didn't establish fewer repeated mistakes in both pairs. If both versions corrected the request immediately, there is no measured advantage.", "",
              "Success requires actual pwd output from the sandbox, a valid timeout, no project changes and confirmed cleanup. "
              "The supplied failure isn't counted as an AI mistake. Missing or failed runs stay in the results; there are no replacements.", "",
              "Two pairs of one simple request don't establish general repair skill or time saved. Billing, my review time and adoption remain unknown or pending. "
              "The [shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111) explains the wider workflow.", ""]
    (path / "comparison.md").write_text("\n".join(lines))
    return result


def execute(path):
    plan = load_plan(path)
    baseline.claim(path, "recovery-" + path.name)
    if any((path / "cells" / str(c["position"]) / "attempt.json").exists() for c in plan["cells"]):
        raise Stop("invalid_input", "A recovery cell already started")
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
            directory, result = run_cell(path, cell, plan)
            record.update(status="finished", run_id=directory.name, receipt_sha256=digest((directory / "result.json").read_bytes()))
            durable_json(path / "progress.json", progress)
            summarize(path)
            if result["outcome"] in ("cancelled", "blocked", "invalid_input") or result.get("agent_outcome") in ("cancelled", "blocked", "invalid_input"):
                raise Stop(result.get("agent_outcome") or result["outcome"], "The recovery experiment stopped; later cells weren't started")
    except KeyboardInterrupt as error:
        raise Stop("cancelled", "Recovery comparison stopped; later cells weren't started") from error
    finally:
        try:
            summarize(path)
        finally:
            for n, handler in handlers.items():
                signal.signal(n, handler)
    return summarize(path)


def main():
    parser = argparse.ArgumentParser(description="Save, run or inspect a focused live comparison of timeout feedback.")
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
