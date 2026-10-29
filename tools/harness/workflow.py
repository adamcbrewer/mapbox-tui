import argparse
import copy
import json
import re
from pathlib import Path
import shutil
import signal
import tempfile
import time

import baseline
from native_check import check_candidate, prepare_verification
from native_run import ToolCallLimit, session
from native_sandbox import NativeRun, NativeSandbox
import operator_log
from recovery import durable_json
from run import HERE, REPORTS, Stop, digest, now
from sandbox import preflight
from verify import REQUIRED_CHECKS, fingerprint, receipt_matches

WORKFLOW_TOOL_CALLS = 30


def sources():
    return {**baseline.sources(), **{name: digest((HERE / name).read_bytes()) for name in ("workflow.py", "operator_log.py")}}


def prepare(suite, image, config, mode, case_id=None, output_root=None):
    path = baseline.prepare(suite, image, config, output_root=output_root or REPORTS / "workflows/plans", case_id=case_id)
    saved = json.loads((path / "plan.json").read_text())
    plan = saved["definition"]
    plan.update(kind="native-repair-workflow-v1", mode=mode, sources=sources(), checkpoint_limit=4,
                primary_metric="Exact final candidate passes every independent check within the shared limits; operator feedback and attention are separate measurements")
    plan["budgets"]["verification_attempts"] = 2
    plan["budgets"]["tool_calls"] = WORKFLOW_TOOL_CALLS
    durable_json(path / "plan.json", {"definition": plan, "sha256": fingerprint(plan)})
    (path / "plan.md").write_text("# A saved repair workflow\n\n"
        f"Mode: {mode}. Pi keeps its normal tools and standard prompt. Both modes use the same task, model, image, limits and final checker. "
        "Plain Pi receives operator corrections only. Managed Pi may receive one automatic correction request after a failed check. "
        "The checker gives no private examples or known fix to the repair agent.\n\n"
        f"{plan['config']['maxModelRequests']} model requests, {plan['budgets']['tool_calls']} Pi tools, two candidate checks and {plan['budgets']['deadline_seconds']} seconds overall, including checking. "
        "Each request keeps its two-minute limit. A tool-limit stop can still send the saved patch to independent checking within the remaining time, without more AI requests. No transport retries or replacement attempts. "
        "Context is retained across continuations; request and tool counts never reset.\n\n"
        "Operator messages arrive after the current conversation stops. Stop sends cancellation to this run's verified process identity. "
        "Check-ins and attention states must be explicitly recorded. Missing human participation/time stays unknown. "
        "A run without operator feedback isn't evidence of zero attention. My final review remains pending.\n")
    return path


def load_plan(path, execution=True):
    saved = json.loads((path / "plan.json").read_text())
    plan = saved["definition"]
    if (fingerprint(plan) != saved["sha256"] or plan["kind"] != "native-repair-workflow-v1" or
            plan["mode"] not in ("plain", "managed") or plan["checkpoint_limit"] != 4 or
            plan["budgets"]["verification_attempts"] != 2 or execution and plan["sources"] != sources()):
        raise Stop("invalid_input", "The workflow plan or implementation changed; no model request was sent")
    if execution:
        baseline.validate_live_config(plan["config"])
        suite = Path(plan["suite"])
        manifest = baseline.load_suite(suite)
        if digest((suite / "suite.json").read_bytes()) != plan["suite_sha256"] or plan["case"] not in manifest["cases"]:
            raise Stop("invalid_input", "The workflow task changed")
        baseline.validate_case(suite / "cases" / plan["case"]["id"], plan["case"])
    return plan


class WorkflowTools(baseline.LiveTools):
    def __init__(self, run, sandbox, plan, project, scripted=False):
        super().__init__(run, sandbox, plan)
        self.project, self.scripted = project, scripted
        self.open = True
        self.seeded = sandbox.seeded
        self.checkpoints = 0
        self.automatic_feedback = 0
        run.result.update(verification_attempts=[], continuations=[], agent_outcome="not_started", first_repair_passed=False)

    def close(self):
        if self.open:
            self.open = False
            self.sandbox.close()

    def can_continue(self):
        return (self.checkpoints < self.plan["checkpoint_limit"] and self.calls < self.plan["budgets"]["tool_calls"] and
                (self.scripted or self.run.result["model_requests"] < self.plan["config"]["maxModelRequests"]) and
                time.monotonic() < self.run.deadline and self.run.result["agent_outcome"] == "finished")

    def continue_with(self, prompt, origin, event_id=None):
        operator_log.check_stop(self.run.path)
        self.run.result["continuations"].append({"origin": origin, "operator_event": event_id, "prompt": prompt, "at": now()})
        self.run.event("workflow_continuation", origin=origin, operator_event=event_id)
        return {"action": "continue", "prompt": prompt}

    def grade(self):
        self.close()
        if self.run.result.get("snapshot_problem") or self.run.result.get("snapshot_scope_valid") is not True:
            raise Stop("incomplete", "The final source snapshot needs inspection")
        patch = self.run.path / "candidate.patch"
        if not patch.read_bytes().strip():
            self.run.result.update(outcome="incomplete", reason="No allowed source change was left to check")
            return False
        attempts = self.run.result["verification_attempts"]
        if len(attempts) >= 2:
            raise Stop("budget_exhausted", "Both candidate-check attempts were spent")
        attempt_path = self.run.path / "attempts" / str(len(attempts) + 1)
        attempt_path.mkdir(parents=True)
        for name in ("candidate.patch", "candidate-source.bin", "project-snapshot.json"):
            shutil.copyfile(self.run.path / name, attempt_path / name)
        self.run.result.pop("verification", None)
        try:
            check_candidate(self.run, attempt=len(attempts) + 1)
        except Stop as error:
            self.run.result.update(outcome=error.outcome, reason=error.reason)
            if error.outcome != "repair_failed":
                raise
        finally:
            if self.run.result.get("verification"):
                attempts.append(copy.deepcopy(self.run.result["verification"]))
                self.run.event("workflow_candidate_checked", attempt=len(attempts))
        return self.run.result.get("independent_checks_passed") is True

    def reopen(self):
        operator_log.check_stop(self.run.path)
        source = (self.run.path / "candidate-source.bin").read_bytes()
        for child in self.project.iterdir():
            shutil.rmtree(child) if child.is_dir() else child.unlink()
        original = Path(self.plan["suite"]) / "cases" / self.plan["case"]["id"] / "agent"
        shutil.copytree(original, self.project, dirs_exist_ok=True)
        (self.project / self.run.task["allowed_files"][0]).write_bytes(source)
        self.sandbox = NativeSandbox(self.run, self.project)
        self.sandbox.seeded = self.seeded
        self.sandbox.expected = self.plan["case"]["agent_files_sha256"].copy()
        self.open = True
        self.sandbox.start()

    def checkpoint(self, message):
        self.checkpoints += 1
        if self.checkpoints > self.plan["checkpoint_limit"] or self.active:
            raise Stop("budget_exhausted", "The workflow checkpoint limit was reached")
        durable_json(self.run.path / f"checkpoint-{self.checkpoints}.json", message)
        self.run.result["agent_outcome"] = message["agentOutcome"]
        operator_log.check_stop(self.run.path)
        if self.can_continue():
            correction = operator_log.take_feedback(self.run.path)
            if correction:
                return self.continue_with("Operator correction. Keep the original task scope and limits.\n" + correction["message"],
                                          "operator", correction["id"])
        passed = self.grade()
        operator_log.check_stop(self.run.path)
        if self.checkpoints == 1 and passed:
            self.run.result["first_repair_passed"] = True
        if self.run.result["agent_outcome"] not in ("finished", "budget_exhausted"):
            self.run.result.update(outcome=self.run.result["agent_outcome"], independent_checks_passed=False,
                                   reason="The conversation stopped unexpectedly; its candidate checks are recorded separately")
            return {"action": "stop"}
        if (passed or self.plan["mode"] == "plain" or self.automatic_feedback or not self.can_continue() or
                len(self.run.result["verification_attempts"]) >= 2):
            return {"action": "stop"}
        self.automatic_feedback += 1
        self.reopen()
        prompt = ("The runner checked your saved candidate and it hasn't established a correct repair. "
                  "Re-examine the expected behaviour and your source change, then make one further repair attempt. "
                  "Keep the original scope and limits. No private test inputs or known fix are provided. "
                  "Use the visible tests when helpful. The remaining request, tool and time budget is unchanged.")
        return self.continue_with(prompt, "runner")


def execute(path, scripted_calls=None):
    plan = load_plan(path)
    run = NativeRun(REPORTS / "workflows", plan["image"])
    run.deadline = run.started + plan["budgets"]["deadline_seconds"]
    run.result.update(workflow="native-repair-workflow", mode=plan["mode"], plan=str(path), plan_sha256=fingerprint(plan),
                      candidate_source="supplied" if scripted_calls is not None else "AI", config=plan["config"],
                      model=plan["config"]["model"] if scripted_calls is None else "supplied (no AI)",
                      provider=plan["config"]["provider"], model_requests=0, dispatches=[], provider_usage=[],
                      auth=plan["config"].get("auth") if scripted_calls is None else None,
                      reasoning="low" if scripted_calls is None else "off", pi_version="1.2.0",
                      native_environment=plan.get("native_environment"),
                      model_cost_usd=0 if scripted_calls is not None else None, budgets=plan["budgets"], independent_checks_passed=False)
    if plan.get("comparison"):
        run.result["comparison"] = plan["comparison"]
    handlers = {n: signal.signal(n, run.cancel) for n in (signal.SIGINT, signal.SIGTERM)}
    tools = None
    try:
        baseline.claim(path, run.path.name)
        operator_log.create(run.path)
        print(f"Run folder: {run.path}", flush=True)
        prepare_verification(run, plan["case"])
        if run.result["evaluator"] != plan["evaluator"]:
            raise Stop("invalid_input", "The checking rules differ from the saved workflow")
        preflight(run)
        run.prepare()
        if any(run.result[key] != plan[key] for key in ("runtime", "environment", "seccomp_sha256")):
            raise Stop("blocked", "The runtime differs from the saved workflow")
        with tempfile.TemporaryDirectory(prefix="mapbox-calibration-workflow-") as temporary, tempfile.TemporaryDirectory(prefix="mapbox-workflow-session-") as node:
            project = Path(temporary)
            shutil.copytree(Path(plan["suite"]) / "cases" / plan["case"]["id"] / "agent", project, dirs_exist_ok=True)
            sandbox = NativeSandbox(run, project)
            tools = WorkflowTools(run, sandbox, plan, project, scripted_calls is not None)
            try:
                sandbox.start()
                start = {"workflowController": True, "task": plan["context"]["task"], "prompt": plan["context"]["prompt"]}
                if scripted_calls is None:
                    start.update(mode="live", config=plan["config"], authDir=str(Path.home() / ".pi/agent"))
                else:
                    start["calls"] = scripted_calls
                try:
                    session(run, tools, start, Path(node))
                except ToolCallLimit:
                    tools.close()
                    tools.checkpoint({"agentOutcome": "budget_exhausted", "limit": "tool_calls"})
            finally:
                tools.close()
        if not tools.checkpoints:
            raise Stop("incomplete", "Pi didn't reach a recorded workflow checkpoint")
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason, independent_checks_passed=False)
        if run.result.get("agent_outcome") == "not_started" and (run.result["model_requests"] or run.result.get("tool_calls")):
            run.result["agent_outcome"] = error.outcome
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="The workflow was stopped; partial evidence stays saved", independent_checks_passed=False)
    except (OSError, ValueError, KeyError, TypeError) as error:
        run.result.update(outcome="blocked", reason=str(error), independent_checks_passed=False)
    finally:
        try:
            run.finalizing = True
            elapsed = time.monotonic() - run.started
            run.result.update(ended_at=now(), duration_seconds=round(elapsed, 4))
            if run.result.get("independent_checks_passed") and elapsed > plan["budgets"]["deadline_seconds"]:
                run.result.update(outcome="timeout", independent_checks_passed=False, first_repair_passed=False,
                                  reason="The saved checks completed, but the workflow exceeded its overall deadline")
            try:
                run.result["usage_summary"] = baseline.token_summary(run.result["provider_usage"], len(run.result["dispatches"]))
            except (ValueError, KeyError, TypeError):
                run.result["usage_summary"] = {"complete": False}
            try:
                if (run.path / "operator.json").exists():
                    operator_log.close(run.path)
                    run.result["operator"] = operator_log.summary(run.path)
                else:
                    run.result["operator"] = None
            except (OSError, ValueError, KeyError, TypeError, Stop):
                run.result["operator"] = {"timing_complete": False, "attention_seconds": None, "problem": "Operator evidence needs inspection"}
            run.event("workflow_finished", outcome=run.result["outcome"])
            (run.path / "workflow.md").write_text(render(run.result))
        finally:
            for n, handler in handlers.items():
                signal.signal(n, handler)
    return run.path, run.result


def validated_result(directory, reports=None):
    result = json.loads((directory / "result.json").read_text())
    plan_path = Path(result["plan"])
    if reports is not None and plan_path.is_relative_to("reports/harness"):
        plan_path = reports / plan_path.relative_to("reports/harness")
    plan = load_plan(plan_path, execution=False)
    if (result["run_id"] != directory.name or result["plan_sha256"] != fingerprint(plan) or result["budgets"] != plan["budgets"] or
            result.get("native_environment") != plan.get("native_environment") or
            result["mode"] != plan["mode"] or result["config"] != plan["config"] or result["image"] != plan["image"] or
            not 0 <= result["model_requests"] <= plan["config"]["maxModelRequests"] or
            not 0 <= result.get("tool_calls", 0) <= plan["budgets"]["tool_calls"]):
        raise Stop("invalid_input", "The workflow result doesn't match its saved plan")
    if result["model_requests"]:
        reservations = json.loads((directory / "reservations.json").read_text())
        context = json.loads((directory / "context.json").read_text())
        if (len(reservations) != result["model_requests"] or len(result["dispatches"]) != len(set(result["dispatches"])) or
                any(item["request"] != n or any(item[k] != plan["config"][k] for k in ("provider", "model"))
                    for n, item in enumerate(reservations, 1)) or
                any(type(n) is not int or not 1 <= n <= len(reservations) for n in result["dispatches"]) or
                {key: context[key] for key in baseline.CONTEXT} != plan["context"] or
                context["permissions"] != {"network": True, "childProcess": False}):
            raise Stop("invalid_input", "The model requests or actual context don't match the plan")
    elif result["dispatches"]:
        raise Stop("invalid_input", "Model dispatches have no reservations")
    passed = False
    partial = False
    for index, record in enumerate(result.get("verification_attempts", []), 1):
        if not isinstance(record.get("run_id"), str) or not re.fullmatch(r"[0-9a-f]{32}", record["run_id"]):
            raise Stop("invalid_input", "Invalid candidate-check identity")
        child = (reports or REPORTS) / "verification/runs" / record["run_id"]
        if child.is_symlink() or any(p.is_symlink() for p in child.rglob("*")):
            raise Stop("invalid_input", "Linked checking evidence needs inspection")
        raw = (child / "result.json").read_bytes()
        checked = json.loads(raw)
        failed = checked["outcome"] == "repair_failed"
        complete = checked["outcome"] in ("repository_verification_passed", "repair_failed")
        patch = (directory / "attempts" / str(index) / "candidate.patch").read_bytes()
        observed_checks = [{**{k: c.get(k) for k in ("id", "outcome", "exit_code")},
                            **({"viewer_artifacts": c["viewer_artifacts"]} if "viewer_artifacts" in c else {})}
                           for c in checked["checks"] if c["id"] in REQUIRED_CHECKS]
        if (digest(raw) != record["receipt_sha256"] or
                checked["parent_run_id"] != result["run_id"] or checked["attempt"] != index or
                any(checked[key] != result[key] for key in ("image", "evaluator_sha256")) or
                checked["evaluator"] != plan["evaluator"] or checked["evaluator"]["task_sha256"] != result["task_sha256"] or
                checked.get("task_sha256") not in (None, result["task_sha256"]) or
                checked.get("task") not in (None, plan["case"]["id"]) or
                record["outcome"] != checked["outcome"] or record["patch_sha256"] != digest(patch) or
                checked.get("patch_sha256") not in (None, digest(patch)) or record["checks"] != observed_checks or
                record["containers_created"] != len(checked["containers"]) or
                record["containers_removed"] != sum(c["removed"] for c in checked["containers"])):
            raise Stop("invalid_input", "Candidate-check evidence no longer matches the workflow")
        if complete:
            if not receipt_matches(checked, child, allow_failed=failed):
                raise Stop("invalid_input", "A completed candidate check lacks valid evidence")
        else:
            if checked["outcome"] not in ("blocked", "cancelled", "timeout", "incomplete", "invalid_input", "budget_exhausted") or result.get("independent_checks_passed"):
                raise Stop("invalid_input", "Partial checking evidence cannot establish a successful repair")
            partial = True
            for check in checked["checks"]:
                if check["id"] not in REQUIRED_CHECKS:
                    continue
                for stream in ("stdout", "stderr"):
                    if check.get(stream) != f"{check['id']}.{stream}":
                        raise Stop("invalid_input", "Partial check log identity changed")
                    expected = check.get(stream + "_sha256")
                    if expected and digest((child / check[stream]).read_bytes()) != expected:
                        raise Stop("invalid_input", "Partial checking output changed")
        passed = complete and not failed
    if len(result.get("verification_attempts", [])) > 2:
        raise Stop("invalid_input", "The workflow exceeded its candidate-check limit")
    if result.get("independent_checks_passed") is True:
        if (not passed or result["outcome"] != "native_verification_passed" or result["duration_seconds"] > plan["budgets"]["deadline_seconds"] or
                not result["containers"] or not all(c["removed"] for c in result["containers"]) or
                result["agent_outcome"] not in ("finished", "budget_exhausted") or
                result["candidate_source"] == "AI" and not result["dispatches"] or
                result.get("snapshot_scope_valid") is not True or result.get("snapshot_problem") or
                digest((directory / "candidate.patch").read_bytes()) != result["verification_attempts"][-1]["patch_sha256"]):
            raise Stop("invalid_input", "The final success claim isn't supported by its evidence")
        first = result["verification_attempts"][0]["outcome"] == "repository_verification_passed" and not result.get("continuations")
        if result.get("first_repair_passed") is not first:
            raise Stop("invalid_input", "The first-repair claim doesn't match the checked attempts and continuations")
    if result.get("continuations"):
        operator = json.loads((directory / "operator.json").read_text())
        delivered = [c["operator_event"] for c in result["continuations"] if c["origin"] == "operator"]
        ids = {e["id"] for e in operator["events"] if e["action"] == "feedback"}
        if (len(delivered) != len(set(delivered)) or any(i not in ids or i not in operator["consumed"] for i in delivered) or
                sum(c["origin"] == "runner" for c in result["continuations"]) > (1 if result["mode"] == "managed" else 0)):
            raise Stop("invalid_input", "Workflow continuations don't match their recorded origin")
    result["partial_verification_evidence"] = partial
    return result


def render(result):
    passed = result.get("independent_checks_passed") is True and result["outcome"] == "native_verification_passed"
    attempts = result.get("verification_attempts", [])
    agent = result.get("agent_outcome", "not_started")
    if agent == "not_started" and (result["model_requests"] or result.get("tool_calls")):
        agent = result["outcome"]
    labels = {"finished": "Pi finished its reply", "not_started": "No repair conversation started",
              "budget_exhausted": "Pi reached a limit before completing its reply", "cancelled": "The conversation was stopped",
              "blocked": "The conversation couldn't finish", "timeout": "The time limit stopped the conversation"}
    lines = ["# " + ("The saved fix passed the full checks" if passed else "This run didn't deliver a fully checked fix"), "",
             ("Pi chose the repair steps." if result.get("dispatches") else
              "No model dispatch was observed." if result["candidate_source"] == "AI" else "I supplied the steps; no repair AI ran.") +
             " Mode: " + result["mode"] + ".", "",
             f"Candidate checks: {len(attempts)}. Model requests: {result['model_requests']}. Pi tools: {result.get('tool_calls', 0)}. "
             f"Elapsed time: {result['duration_seconds']} seconds.", ""]
    if result.get("task") == "cli-inspect-v1":
        lines += ["This extends the import workflow to an agreed CLI/MCP contract. CLI inspect without a selector must return point 0. "
                  "Standalone MCP still requires exactly one explicit selector. Explicit selectors, errors, structured results and offline ANSI-free maps must remain usable.", "",
                  "The planted bug defaults CLI inspection to point 1. Separate examples include a one-point file, option placement and distance zero. "
                  "Schemas and tests are frozen outside the repair scope. No live viewer is contacted.", ""]
    if result.get("task") == "viewer-modal-v1":
        lines += ["This is the viewer interaction workflow in #110. The planted bug drops import completion while a dialog is open. "
                  "The saved repair must complete the accepted import, select its visible synthetic data and keep the dialog open. "
                  "The checker also uses extra modal/terminal examples kept outside Pi's project. All viewers are test-owned, isolated and offline.", "",
                  "Structured assertions prove completion and imported state. Retained captures need human review for appearance. "
                  "Missing terminal tools or uncertain state block the check. A pending response alone is not success.", ""]
    for index, attempt in enumerate(attempts, 1):
        lines.append(f"Attempt {index}: " + ("Passed all required checks." if attempt["outcome"] == "repository_verification_passed" else
                                          "Failed the required checks." if attempt["outcome"] == "repair_failed" else "Checking couldn't finish."))
        check_labels = {"baseline": "Original code", "seed": "Planted bug, expected failure", "candidate": "Proposed repair",
                        "baseline-regression": "Original code, separate examples", "seed-regression": "Planted bug, separate examples, expected failure",
                        "candidate-regression": "Proposed repair, separate examples", "candidate-format": "Formatting", "candidate-suite": "Repository tests"}
        lines += ["", "| Check | Observed result |", "| --- | --- |"]
        observed = {check["id"]: check for check in attempt.get("checks", [])}
        for identity in REQUIRED_CHECKS:
            check = observed.get(identity)
            status = "Not run" if check is None else check["outcome"]
            log = ("../../../verification/runs/" + attempt["run_id"] + "/" + identity + ".stdout") if check else None
            label = "Passed" if status == "passed" else "Failed as intended" if status == "assertion_failed" and identity.startswith("seed") else "Failed" if status in ("assertion_failed", "failed", "build_failed") else status
            lines.append("| " + check_labels[identity] + " | " + ("[" + label + "](" + log + ")" if log else label) + " |")
        if result.get("task") == "viewer-modal-v1":
            lines += ["", "Retained viewer evidence:"]
            for check in attempt.get("checks", []):
                for file in sorted(check.get("viewer_artifacts", {})):
                    if file.endswith(("before-import.png", "after-import.png", "states.jsonl")):
                        target = "../../../verification/runs/" + attempt["run_id"] + "/captures/" + check["id"] + "/" + file
                        lines.append("- [" + check["id"] + " / " + file + "](" + target + ")")
        lines += ["", f"Read the [saved candidate patch](attempts/{index}/candidate.patch) and [separate check receipt](../../../verification/runs/" + attempt["run_id"] + "/result.json).", ""]
    lines += ["", labels.get(agent, "The conversation didn't complete") + ". " + result.get("reason", ""), "",
              "Operator feedback delivered: " + str(sum(c["origin"] == "operator" for c in result.get("continuations", []))) + ". "
              "This doesn't establish zero human attention. Explicit attention records are self-reported; missing time stays unknown.", "",
              "Model/execution billing and my review time are unknown unless recorded. The held-out status is in the saved plan. "
              "A passing repair doesn't imply my approval or general real-world repair skill. "
              "Use operator_log.py with this run folder for status, corrections and attention records. "
              "Keep the original receipts, candidate attempts, conversations and logs together locally.", ""]
    operator = result.get("operator") or {}
    lines += [f"Recorded human check-ins: {operator.get('check_ins', 'unknown')}. "
              f"Submitted corrections: {operator.get('corrections_submitted', 'unknown')}."]
    attention = operator.get("attention_seconds")
    lines += ["Explicitly recorded monitoring/review time: " + ("unknown." if attention is None else f"{attention} seconds."),
              "This covers recorded intervals only. Earlier or unrecorded attention stays unknown.", ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Prepare, run or inspect a bounded plain/managed Pi repair workflow.")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--prepare", action="store_true")
    mode.add_argument("--run", type=Path)
    mode.add_argument("--report", type=Path)
    parser.add_argument("--suite", type=Path)
    parser.add_argument("--image")
    parser.add_argument("--config", type=Path)
    parser.add_argument("--mode", choices=("plain", "managed"), default="plain")
    parser.add_argument("--case")
    parser.add_argument("--demo", choices=("retry", "exhausted"))
    args = parser.parse_args()
    try:
        if args.prepare:
            if not all((args.suite, args.image, args.config)):
                parser.error("--prepare needs --suite, --image and --config")
            print(prepare(args.suite, args.image, args.config, args.mode, args.case))
            return 0
        if args.report:
            result = validated_result(args.report)
            if (args.report / "operator.json").exists():
                result["operator"] = operator_log.summary(args.report)
            (args.report / "workflow.md").write_text(render(result))
            print(args.report / "workflow.md")
            return 0
        calls = None
        if args.demo:
            edits = ([{"oldText": "if j > 0 {", "newText": "if j > s.Start {"}, {"oldText": 'format := "fit"', "newText": 'format := "FIT"'}]
                     if args.demo == "retry" else [{"oldText": 'format := "fit"', "newText": 'format := "Fit"'}])
            calls = [{"name": "edit", "args": {"path": "go/import.go", "edits": [{"oldText": 'format := "FIT"', "newText": 'format := "fit"'}]}}, None,
                     {"name": "edit", "args": {"path": "go/import.go", "edits": edits}}, None]
        directory, result = execute(args.run, calls)
        print(directory / "workflow.md")
        return 0 if result.get("independent_checks_passed") and result["outcome"] == "native_verification_passed" else 1
    except (Stop, OSError, ValueError, KeyError, TypeError) as error:
        print(error.reason if isinstance(error, Stop) else str(error))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
