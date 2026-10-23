import argparse
import ctypes
import hashlib
import html
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile

from run import HERE, REPORTS, ROOT, digest, now, write_json

STAGES = ("Define", "Connect", "Verify", "Recover", "Compare", "Use")
STATES = ("planned", "active", "blocked", "awaiting-review", "reviewed")
EVIDENCE_FILES = ("result.json", "task.json", "events.jsonl", "candidate.patch", "seed.patch", "context.json", "pi-session.json")
RUN_FOLDERS = ("runs", "sandbox-preflight/runs", "sandbox-probes/runs", "pi/runs")


def escape(value):
    return html.escape(str(value), quote=True)



STATE_LABELS = {
    "planned": "Not started", "active": "In progress", "blocked": "Needs attention",
    "awaiting-review": "Waiting for our review", "reviewed": "Reviewed together",
}
EXAMPLES = {
    "good": ("Try a repair we know works", "Restore the removed import code.",
             "The import test should pass again.", "Check that a working repair can pass."),
    "empty": ("Try making no repair", "Leave the deliberately broken code unchanged.",
              "The import test should still fail.", "Check that doing nothing cannot count as a repair."),
    "wrong": ("Try a change that does not fix the bug", "Change an unrelated detail in the importer.",
              "The import test should still fail.", "Check that changing code alone cannot count as a repair."),
}
FAULT_EXAMPLES = {
    "missing-source": ("Try checking an incomplete source copy", "Remove the importer from the disposable copy before checking it.",
                       "Stop and identify the missing source file.", "Missing code must not count as a reproduced bug or a failed repair."),
    "missing-dependency": ("Try checking without the required dependencies", "Use an empty, temporary Go dependency cache with downloads disabled.",
                           "Stop because the original code cannot be built.", "A setup problem must not count as a failed repair. Your normal dependency cache stays untouched."),
    "timeout": ("Try a command that takes too long", "Run a Python command that waits for 60 seconds, with only one second allowed.",
                "Stop the command at the time limit and keep its logs.", "Prove the runner stops waiting and records an unfinished check. This example does not run an import test."),
}
OUTCOMES = {
    "calibration_passed": "The proposed repair passed this check.",
    "scripted_loop_passed": "The real Pi tool loop completed using prepared responses. No AI model ran.",
    "live_repair_passed": "The AI-written repair passed the fixed import check. Human review is still pending.",
    "repair_failed": "The proposed repair did not pass this check.",
    "calibration_failed": "The setup did not produce the expected working and broken examples.",
    "blocked": "We couldn’t complete the checks. This does not tell us whether the repair works.",
    "invalid_input": "The request could not be used. Check the patch file and which changes are allowed.",
    "timeout": "The run reached its time limit before finishing.",
    "cancelled": "The run was stopped before finishing.",
    "incomplete": "No final result was recorded. The run may still be working or may have stopped unexpectedly.",
    "budget_exhausted": "A tool or model limit was reached. This does not count as a completed repair.",
}
GLOSSARY = {
    "Harness": "The software around the AI that controls the task, tools, checks and saved evidence.",
    "Model / agent / Pi": "The model is the AI. An agent uses a model and tools to attempt a task. Pi supplies that loop. Prepared responses can test its wiring without calling a model.",
    "Checker / verifier": "Code that runs required checks and records their results. It does not understand or approve the code like a person.",
    "Calibration": "Trying examples with known answers to see whether our checking process reports them correctly.",
    "Baseline": "The starting code used for comparison. Here it works; a real repair task may start with broken code.",
    "Seed patch": "A change that deliberately plants a bug in the temporary copy.",
    "Candidate patch": "The proposed repair we are testing. Prepared examples use a supplied patch; live Pi runs save the model's edit.",
    "Patch": "A file listing code lines to add or remove.",
    "Fixture": "Prepared example input used by a test, rather than your personal activity data.",
    "Run / attempt / check": "A run is one execution of a task. An attempt is one proposed solution within it. A check is an individual test or validation step. This pilot tries one solution per run.",
    "Run ID / hash": "A run ID labels its folder; the short ID beside Receipt helps you find it. A hash is a fingerprint identifying exact file contents. They are different things.",
    "Receipt": "The saved result summary and references to its evidence. It is not human approval.",
    "Fault example": "A deliberately introduced setup problem or slow command. It tests how the runner handles trouble, not whether a repair works.",
    "Podman": "The container tool chosen for a disposable test environment in Connect (#104). Readiness inspects its reported features. Separate live demonstrations check dummy access denials, kernel settings and cleanup.",
    "Sandbox readiness": "Checking whether the container runtime reports the prerequisites we need. This does not prove that a container blocks access or enforces limits.",
}
FILE_GUIDE = {
    "tools/harness/run.py": "Conducts the experiment and saves its evidence.",
    "tools/harness/sandbox.py": "Checks local rootless Podman prerequisites. With an immutable prepared image ID, it can check a trusted repair in containers. No host execution fallback.",
    "tools/harness/container.py": "Creates and inspects restricted containers, runs fixed checks, then removes each container and confirms it is absent.",
    "tools/harness/probes.py": "Runs trusted dummy access checks or deliberately slow commands inside the container. These are separate from repair-quality checks.",
    "tools/harness/pi_run.py": "Runs prepared replies or the configured subscription through four controlled tools. Python owns the disposable source, limits and sandboxed checks. A live attempt can stop before inference if its limits cannot be enforced.",
    "tools/harness/pi/config.local.json": "Your ignored local provider, model and subscription settings. Keep model choice and request/output limits explicit. Credentials live separately in Pi's private auth file.",
    "context.json / pi-session.json": "The prompt, tool definitions and Pi conversation. Check the mode: prepared replies test wiring; live runs use the recorded subscription model.",
    "tools/harness/task.json": "The instruction sheet: starting code, expected behaviour, allowed changes and required test.",
    "tools/harness/fixtures/*.patch": "The deliberate bug and prepared example repairs.",
    "tools/harness/report.py": "Builds this HTML page from saved records. It does not run tests.",
    "tools/harness/progress.json": "Records our stage, decisions and pending discussion. Test results do not update our approval.",
    "tools/harness/test_harness.py": "Tests the Python machinery itself.",
    "tools/harness/README.md": "Commands and instructions for using and reviewing this pilot.",
    "reports/harness/index.html": "The latest stage report, with links to saved stage pages.",
    "reports/harness/define.html / connect.html": "Separate stage reports. Rebuilding Connect leaves the saved Define page unchanged.",
    "reports/harness/snapshots/<stage>/": "Saved progress, provenance and copied evidence for one stage. Include this folder for original-file links and logs; the viewer's five file types are embedded in the HTML.",
    "reports/harness/runs/<run-id>/": "Evidence from one experiment. Within it, task.json preserves the instructions, seed.patch preserves the deliberately broken change, and candidate.patch preserves the proposed repair.",
    "reports/harness/sandbox-preflight/runs/<run-id>/": "Separate runtime-readiness receipts and Podman inspection logs. These are not repair attempts or proof of container isolation.",
    "result.json (inside a run folder)": "The final summary: outcome, timings and exactly what was checked. Receipt links open the embedded copy in the evidence drawer.",
    "events.jsonl (inside a run folder)": "A chronological diary, with one event per line: a command started, finished, or a test result was recorded.",
    "*.stdout / *.stderr (inside a run folder)": "Command output and diagnostics. Usually only needed to investigate a problem. baseline and seed refer to the original and deliberately broken code; candidate refers to the proposed repair. scope checks allowed files; metadata checks file operations such as renaming or deleting; apply records applying a patch. go-version records the compiler version.",
    "snapshot.stdout": "An exception to the readable logs: this contains the archived source copy. snapshot.stderr contains any archive-command diagnostics.",
    "reports/harness/*.log": "Checks run on the harness implementation, separate from the individual experiments.",
}


def definitions(entries):
    return "".join(f"<dt>{escape(term)}</dt><dd>{escape(meaning)}</dd>" for term, meaning in entries.items())


def history(rows, label):
    if not rows:
        return ""
    return f'<details class="history"><summary>{escape(label)} ({len(rows)})</summary>{"".join(rows)}</details>'


def embedded_evidence(reports, link_root=None):
    evidence = {}
    for folder in RUN_FOLDERS:
        for receipt in sorted((reports / folder).glob("*/result.json")):
            files = {}
            for name in EVIDENCE_FILES:
                path = receipt.with_name(name)
                entry = {"href": path.relative_to(link_root or reports).as_posix(), "text": None}
                try:
                    if path.is_symlink():
                        entry["unavailable"] = "Linked files are not embedded."
                    else:
                        entry["text"] = path.read_bytes().decode("utf-8")
                except FileNotFoundError:
                    entry["unavailable"] = "Not recorded for this run."
                except (OSError, UnicodeError):
                    entry["unavailable"] = "This file could not be read as UTF-8 text."
                files[name] = entry
            evidence[receipt.relative_to(link_root or reports).as_posix()] = {
                "run_id": receipt.parent.name, "files": files}
    return json.dumps(evidence, ensure_ascii=True).replace("<", "\\u003c")


def render_run(path, reports, run, link_root=None, annotation=None):
    candidate = run.get("candidate")
    title, tried, expected, purpose = EXAMPLES.get(str(candidate), (
        "Check a supplied repair" if candidate else "Request could not be loaded",
        "Use the supplied patch file.", "A usable request is needed before we can judge a repair.",
        "Keep input or tool problems separate from failed repairs.",
    ))
    fault = run.get("fault_injected")
    if isinstance(fault, str) and fault == run.get("fault") and fault in FAULT_EXAMPLES:
        title, tried, expected, purpose = FAULT_EXAMPLES[fault]
    elif run.get("fault"):
        title = "Requested a setup-failure example"
        tried = "Start the example. There is no record that the deliberate problem was introduced."
        expected = "Introduce the requested problem, then check how the runner handles it."
        purpose = "Keep a requested experiment separate from what actually ran."
    recorded_checks = run.get("checks", [])
    known = (run.get("schema_version") == 1 and isinstance(recorded_checks, list)
             and all(isinstance(check, dict) and isinstance(check.get("id"), str)
                     and isinstance(check.get("outcome"), str) for check in recorded_checks))
    if known and run.get("outcome") in ("calibration_passed", "scripted_loop_passed", "live_repair_passed", "repair_failed"):
        stages = [check for check in recorded_checks if check.get("id") in ("baseline", "seed", "candidate")]
        failed = run["outcome"] == "repair_failed"
        candidate_outcomes = ("assertion_failed", "build_failed") if failed else ("passed",)
        candidate_exit = stages[-1].get("exit_code") if stages else None
        known = ([check.get("id") for check in stages] == ["baseline", "seed", "candidate"] and
                 [check.get("outcome") for check in stages[:2]] == ["passed", "assertion_failed"] and
                 stages[2].get("outcome") in candidate_outcomes and
                 type(stages[0].get("exit_code")) is int and stages[0]["exit_code"] == 0 and
                 type(stages[1].get("exit_code")) is int and stages[1]["exit_code"] != 0 and
                 type(candidate_exit) is int and (candidate_exit != 0 if failed else candidate_exit == 0))
        if run.get("outcome") == "scripted_loop_passed":
            known = known and run.get("startup_verified") is True and run.get("tool_calls") == 6
        if run.get("outcome") == "live_repair_passed":
            known = (known and run.get("startup_verified") is True and run.get("workflow") == "pi-live-tools"
                     and isinstance(run.get("model_requests"), int) and run["model_requests"] > 0
                     and bool(run.get("model")) and bool(run.get("provider")))
    outcome = run.get("outcome") if known else None
    result = OUTCOMES.get(str(outcome), "Unreadable record or unknown result. Inspect the receipt before drawing a conclusion.")
    expected_failure = (candidate in ("empty", "wrong") and outcome == "repair_failed" and
                        stages[2]["outcome"] == "assertion_failed")
    if expected_failure:
        result += " This failure is the expected answer for this example."
    if outcome == "blocked" and run.get("reason") == "Missing executable: go":
        title = "The check could not start without Go"
        tried = "Start the requested run without access to the Go test tool."
        expected = "Stop and explain that the tests could not run."
        purpose = "Distinguish a missing tool from a repair that was tested and failed."
        result = "Go was unavailable, so the repair could not be checked."
    if outcome == "blocked" and run.get("blocker") == "missing_source":
        result = "A required source file was missing, so no import tests ran. The repair has not been judged."
    if outcome == "blocked" and run.get("blocker") == "missing_dependency":
        result = "Required Go dependencies were unavailable, so the original code could not be built. The repair has not been judged."
    if fault == "timeout" and outcome == "timeout":
        result = "The runner stopped at its time limit. The repair has not been judged; see the steps to find which command stopped."
    checks = []
    names = {"baseline": "Original code", "seed": "Deliberately broken code", "candidate": "Code after the proposed repair",
             "timeout-probe": "Deliberately slow command (not an import test)"}
    answers = {"passed": "test passed", "assertion_failed": "test failed", "build_failed": "code could not be built", "blocked": "could not check",
               "incomplete": "no final result", "timeout": "time limit reached", "cancelled": "stopped"}
    for check in recorded_checks if known else []:
        if check.get("id") in names:
            tone = "pass" if check.get("outcome") == "passed" else "attention"
            checks.append(f'<li class="{tone}"><span>{names[check["id"]]}</span><strong>{escape(answers.get(check.get("outcome"), "unknown result"))}</strong></li>')
    duration = run.get("duration_seconds")
    elapsed = f"{duration:.2f} seconds" if isinstance(duration, (int, float)) else "Time not recorded"
    link = escape(path.relative_to(link_root or reports).as_posix())
    run_id = escape(path.parent.name)
    execution = ""
    if run.get("execution_mode") == "rootless-podman":
        containers = run.get("containers")
        cleanup = "Container cleanup records are unavailable."
        if isinstance(containers, list):
            removed = sum(isinstance(item, dict) and item.get("removed") is True for item in containers)
            cleanup = f"Container records: {len(containers)}. Removal confirmed: {removed}."
        execution = (f'<p><strong>Execution:</strong> requested rootless Podman with image <code>{escape(run.get("image"))}</code>.</p>'
                     f'<p>{escape(cleanup)} '
                     'A passing repair check does not establish the full isolation boundary. Review the separate sandbox demonstrations below.</p>')
    if run.get("workflow") == "pi-scripted-tools":
        title = "Pi tools: prepared working repair" if candidate == "good" else "Pi tools: prepared ineffective repair"
        tried = "Use the real Pi SDK to read/search, reject a host-file read, edit once, replay that edit and request the approved test."
        expected = "A working repair passes; an ineffective repair fails. Both must refuse the host-file read and clean up containers."
        purpose = "Check tool wiring before asking an AI to choose actions. The responses and repairs are prepared, with zero model calls."
        if outcome in ("cancelled", "timeout"):
            title = "Pi tools: interrupted run"
            expected = "An interrupted check is unfinished. Inspect container cleanup and the separate Pi-process cancellation evidence."
    identity = ""
    tokens = ""
    if run.get("workflow") == "pi-scripted-tools":
        identity = '<p class="meta"><output>Model: none (prepared responses). Tokens used: 0.</output></p>'
    if run.get("workflow") == "pi-live-tools":
        title = "Pi: model repair attempt"
        if outcome in ("blocked", "invalid_input"):
            title = "Pi: subscription request blocked"
            result = run.get("reason", result)
        tried = "Let the configured subscription model investigate and propose one edit using the four controlled tools."
        expected = "The original passes, the planted bug fails, and an effective model repair passes the fixed sandboxed test."
        purpose = "Trace model decisions through enforced tools to an actual check. One passing fixture does not prove broader repair ability."
        usage = run.get("model_usage", [])
        output = sum(item.get("usage", {}).get("output", 0) for item in usage if isinstance(item, dict))
        model = f'{escape(run.get("provider") or "unknown")} / {escape(run.get("model") or "not selected")}'
        identity = (f'<p class="meta"><output><strong>Model:</strong> {model}. Subscription. '
                    f'{run.get("model_requests", 0)} model request attempts.</output></p>')
        summary = run.get("token_summary")
        if isinstance(summary, dict):
            totals = summary["totals"]
            identity += (f'<p class="model-usage"><output><strong>Recorded tokens: {totals["totalTokens"]:,}</strong>. '
                         f'{totals["inputTokens"]:,} input + {totals["outputTokens"]:,} output. '
                         f'{"Complete" if summary["complete"] else "Partial"} usage record.</output></p>')
            reasoning = totals["reasoningTokens"]
            reasoning_text = f"{reasoning:,}" if isinstance(reasoning, int) else "not reported"
            cap = run.get("budgets", {}).get("output_tokens_per_request")
            tokens = (f'<p><output>Token receipts: {summary["requests_with_usage"]} requests with usage; '
                      f'{summary["requests_without_usage"]} dispatched requests without usage. '
                      f'Recorded totals are {"complete" if summary["complete"] else "partial"}.</output></p>'
                      f'<p><output>Cached input: {totals["cachedInputTokens"]:,}, included in input. '
                      f'Reasoning: {reasoning_text}, included in output. Do not add these subsets again.</output></p>'
                      f'<p><output>Largest response: {summary["largest_response_output_tokens"]:,} output tokens. '
                      f'Responses above the earlier 4,096-token cap: {summary["responses_over_4096_output_tokens"]}.</output></p>'
                      f'<p>Output limit: {"no hard cap; provider default" if cap is None else escape(str(cap))}. '
                      'Each request includes conversation history, so input tokens can accumulate across turns. '
                      'A timeout can leave usage unreported; missing usage is not zero consumption.</p>')
            requests = {row["request"]: row["usage"] for row in run.get("provider_usage", [])}
            if requests:
                rows = ''.join(f'<li><output>Request {escape(number)}: <strong>{usage["totalTokens"]:,} tokens</strong> '
                               f'({usage["inputTokens"]:,} input + {usage["outputTokens"]:,} output).</output></li>'
                               for number, usage in requests.items())
                tokens += f'<p><strong>Usage per request: {model}</strong></p><ul>{rows}</ul>'
        elif run.get("model_requests") == 0:
            identity += '<p class="model-usage"><output>Tokens used: 0. No model requests were made.</output></p>'
        elif output > 0:
            identity += (f'<p class="model-usage"><output>Recorded output tokens: {output:,}. '
                         'Input and total token usage were not reported.</output></p>')
        else:
            identity += '<p class="model-usage"><output>Token usage: not reported. Missing usage does not mean zero tokens.</output></p>'
        tokens += '<p>Actual subscription charge is not reported.</p>'
    matched = outcome in ("calibration_passed", "scripted_loop_passed", "live_repair_passed") or expected_failure
    labels = {"cancelled": "Run cancelled", "timeout": "Time limit reached", "incomplete": "Run unfinished",
              "repair_failed": "Repair check failed", "blocked": "Check could not run", "invalid_input": "Invalid request",
              "budget_exhausted": "Limit reached", "calibration_failed": "Startup checks failed"}
    label = "As expected" if matched else labels.get(outcome, "Unknown result")
    tone = "pass" if matched else "attention"
    note = ""
    if outcome == "cancelled" and annotation:
        containers = run.get("containers", [])
        cleaned = isinstance(containers, list) and bool(containers) and all(
            isinstance(item, dict) and item.get("removed") is True for item in containers)
        cancellation_test = (annotation.get("kind") == "cancellation-test" and
                             run.get("workflow") == "pi-scripted-tools")
        startup_fix = (annotation.get("kind") == "fixed-startup-bug" and run.get("workflow") == "pi-live-tools" and
                       run.get("model_requests") == 0 and run.get("tool_calls") == 0)
        if cleaned and (cancellation_test or startup_fix):
            title, label, result = annotation["title"], annotation["label"], annotation["summary"]
            tone = "pass"
            tried = "Deliberately stop the prepared check." if cancellation_test else "Start the earlier live Pi wiring."
            expected = "Confirm that owned processes stop and containers are removed. The repair check remains unfinished."
            purpose = "Distinguish a completed cancellation check or fixed startup bug from an unresolved repair."
            note = (f'<p>Recorded explanation: {escape(annotation["evidence"])}. '
                    'The original run outcome remains cancelled; this label does not mean its repair passed.</p>')
    mode = "Container requested" if run.get("execution_mode") == "rootless-podman" else "Host runner"
    return f'''<article class="run">
<header class="run-heading"><h3>{escape(title)}</h3><span class="badge {tone}">{escape(label)}</span></header>
<p>{escape(result)}</p>
{identity}
{('<ol class="check-steps">' + ''.join(checks) + '</ol>') if checks else '<p class="meta">No import test results recorded.</p>'}
<p class="meta">{mode}. {escape(elapsed)}. <a href="{link}" data-evidence="{link}">Receipt</a>
<code title="Run ID: {run_id}">{escape(path.parent.name[:8])}</code></p>
<details><summary>Explanation and technical details</summary>
<dl class="explanation"><dt>What we tried</dt><dd>{escape(tried)}</dd><dt>Expected</dt><dd>{escape(expected)}</dd>
<dt>Why it matters</dt><dd>{escape(purpose)}</dd></dl>
{execution}
{tokens}
{note}
<p>Saved status: <code>{escape(run.get('outcome', 'unknown'))}</code>.
Diagnostic: {escape(run.get('reason', 'No final explanation recorded'))}</p>
<p>Candidate label in the saved record: {escape(candidate if candidate is not None else 'not loaded')}.</p>
<p>Requested setup fault: {escape(run.get('fault', 'none'))}.
Fault introduced: {escape(run.get('fault_injected', 'not recorded'))}.</p>
<p>Started: {escape(run.get('started_at', 'not recorded'))}. Human approval is separate from these checks.</p></details>
</article>'''


def render_preflights(reports, link_root=None):
    rows = []
    for path in (reports / "sandbox-preflight/runs").glob("*/result.json"):
        try:
            result = json.loads(path.read_text())
            if not isinstance(result, dict):
                raise ValueError("Receipt must be an object")
        except (ValueError, OSError):
            result = {}
        outcome = result.get("outcome")
        if outcome == "runtime_ready":
            summary = "Podman reports the required features. Container isolation still needs to be demonstrated."
        elif outcome == "blocked":
            summary = "We couldn't check the required runtime. Container execution remains blocked."
        else:
            summary = "No completed readiness decision is available. Inspect the receipt before continuing."
        link = escape(path.relative_to(link_root or reports).as_posix())
        label = "Ready to start" if outcome == "runtime_ready" else "Needs inspection"
        tone = "pass" if outcome == "runtime_ready" else "attention"
        rows.append((str(result.get("started_at", "")), f'''<article class="run">
<header class="run-heading"><h3>Runtime readiness</h3><span class="badge {tone}">{label}</span></header>
<p>{escape(summary)}</p>
<details><summary>What this check established</summary><p>{escape(result.get('reason', 'No final explanation recorded'))}</p>
<p>No candidate code or container was run by this readiness check. A runtime feature report does not prove that access is denied or limits are enforced.</p></details>
<p class="meta">Started: {escape(result.get('started_at', 'not recorded'))}.
<a href="{link}" data-evidence="{link}">Readiness receipt</a></p></article>'''))
    ordered = [row for _, row in sorted(rows)]
    return (ordered[-1] + history(ordered[:-1], "Earlier readiness checks")) if ordered else '<p>No sandbox readiness checks recorded.</p>'


def render_probes(reports, link_root=None):
    rows = []
    for path in (reports / "sandbox-probes/runs").glob("*/result.json"):
        try:
            value = json.loads(path.read_text())
            if not isinstance(value, dict):
                raise ValueError("Receipt must be an object")
        except (ValueError, OSError):
            value = {}
        phase = value.get("phase")
        containers = value.get("containers")
        removed = (isinstance(containers, list) and bool(containers) and
                   all(isinstance(item, dict) and item.get("removed") is True for item in containers))
        checks = value.get("checks", [])
        outcome = value.get("outcome")
        expected = {"boundary": "boundary_passed", "timeout": "timeout", "cancel": "cancelled"}
        confirmed = (str(phase) in expected and value.get("probe_confirmed") is True and
                     removed and outcome == expected.get(str(phase)))
        if not isinstance(checks, list) or not any(
                isinstance(check, dict) and check.get("id") == str(phase) + "-probe" and
                (check.get("exit_code") == 0 and check.get("outcome") == "passed"
                 if phase == "boundary" else check.get("outcome") == outcome)
                for check in checks):
            confirmed = False
        messages = {
            "boundary": "Dummy host-file/network access was denied. Root/source mounts were read-only, and kernel CPU/memory/swap/process settings matched the policy.",
            "timeout": "The deliberately slow command started, hit its deadline, and its container was removed.",
            "cancel": "The deliberately slow command started, was cancelled, and its container was removed.",
        }
        message = messages.get(str(phase)) if confirmed else "No confirmed demonstration is available. Inspect the receipt before drawing a conclusion."
        link = escape(path.relative_to(link_root or reports).as_posix())
        title = {"boundary": "Access and limits", "timeout": "Time limit", "cancel": "Cancellation"}.get(str(phase), "Unknown probe")
        tone = "pass" if confirmed else "attention"
        label = "Confirmed" if confirmed else "Unconfirmed"
        rows.append((str(value.get("started_at", "")), str(phase), f'''<article class="run">
<header class="run-heading"><h3>{title}</h3><span class="badge {tone}">{label}</span></header>
<p>{escape(message)}</p>
{('<p>' + escape(value.get('reason', 'No final explanation recorded')) + '</p>') if not confirmed else ''}
<p class="meta"><a href="{link}" data-evidence="{link}">Probe receipt</a></p>
<details><summary>Recorded status and time</summary><p class="meta">Started: {escape(value.get('started_at', 'not recorded'))}.
Saved outcome: {escape(outcome)}.</p></details></article>'''))
    ordered = sorted(rows)
    latest = {phase: date for date, phase, _ in ordered}
    current = [row for date, phase, row in ordered if date == latest[phase]]
    earlier = [row for date, phase, row in ordered if date != latest[phase]]
    return (''.join(current) + history(earlier, "Earlier demonstrations")) if ordered else '<p>No live sandbox demonstrations recorded.</p>'


def render(reports, progress, *, link_root=None, snapshot=None):
    if progress["status"] not in STATES or progress["stage"] not in STAGES:
        raise ValueError("Unknown progress stage or status")
    milestones = []
    for index, stage in enumerate(STAGES):
        current = stage == progress["stage"]
        label = STATE_LABELS[progress["status"]] if current else (
            "Earlier stage" if index < STAGES.index(progress["stage"]) else "Later")
        marker = ' aria-current="step"' if current else ""
        milestones.append(f'<li{marker}><strong>{stage}</strong><span>{escape(label)}</span></li>')
    milestones = "".join(milestones)
    rows = []
    records = []
    for path in (reports / "runs").glob("*/result.json"):
        try:
            run = json.loads(path.read_text())
            if not isinstance(run, dict):
                raise ValueError("Receipt must be an object")
        except (ValueError, OSError):
            run = {}
        records.append((path, run))
    ordered = sorted(records, key=lambda item: str(item[1].get("started_at", "")))
    container_mode = any(run.get("execution_mode") == "rootless-podman" for _, run in ordered)
    selected = {}
    for path, run in ordered:
        if run.get("candidate") in ("good", "wrong") and not run.get("fault") and (
                not container_mode or run.get("execution_mode") == "rootless-podman"):
            selected[run["candidate"]] = path
    for path, run in ordered:
        if path not in selected.values():
            rows.append(render_run(path, reports, run, link_root))
    current = "".join(render_run(path, reports, run, link_root) for path, run in ordered if path in selected.values())
    records = current + history(rows, "Other experiments, including failed and blocked runs") if ordered else (
        '<p class="empty">No runs recorded. Try the prepared working repair, then rebuild this page.</p>')
    items = lambda values: "".join(f"<li>{escape(value)}</li>" for value in values)
    define = progress["stage"] == "Define"
    pi_history = []
    pi_latest = {}
    for path in sorted((reports / "pi/runs").glob("*/result.json"), key=lambda path: path.stat().st_mtime):
        try:
            run = json.loads(path.read_text())
            if not isinstance(run, dict):
                raise ValueError("Receipt must be an object")
        except (ValueError, OSError):
            run = {}
        candidate = (run.get("candidate"), run.get("outcome") if run.get("outcome") in ("cancelled", "timeout") else "attempt")
        row = render_run(path, reports, run, link_root,
                         annotation=progress.get("run_annotations", {}).get(path.parent.name))
        if candidate in pi_latest:
            pi_history.append(pi_latest[candidate])
        pi_latest[candidate] = row
    pi_rows = "".join(pi_latest.values()) + history(pi_history, "Earlier Pi runs")
    pi_section = f'''<section id="pi" aria-labelledby="pi-title"><h2 id="pi-title">Can Pi use our controlled tools?</h2>
<p>Prepared examples test the tool wiring without AI. Live attempts use the named subscription model and record its requests and usage.</p>
{pi_rows}<details><summary>What to inspect and learn</summary>
<p>In Events, find the refused host-file read and the replayed edit. The source changes once. Compare the good and wrong test results.
Context preserves the prompt and four tool schemas; Pi conversation preserves their requests and replies.</p>
<p>In a live attempt, compare the model's investigation, proposed edit and final test result. Our runner enforces which actions are allowed.
Passing the fixed test, the model saying “done” and human approval are separate.</p>
</details></section>''' if pi_rows and not define else ''
    saved_links = '<a href="index.html">Latest report</a>' + ''.join(
        f'<a href="{stage.lower()}.html">{stage}</a>' for stage in STAGES
        if link_root and (link_root / "snapshots" / stage.lower() / "snapshot.json").is_file())
    saved_nav = f'<nav class="saved-reports" aria-label="Saved stage reports">{saved_links}</nav>' if snapshot else ''
    provenance = ''
    if snapshot:
        source_note = (f'Restored from Git {snapshot["source_ref"][:12]} and the original local evidence. '
                       'The original review status is retained; this is a historical snapshot, not the current project status.'
                       if snapshot.get("restored") else 'Captured from this stage’s local evidence and progress record.')
        provenance = (f'<details><summary>Snapshot source and date</summary><p>{escape(source_note)}</p>'
                      f'<p>Captured {escape(snapshot["captured_at"])}. Presented using the current report design.</p></details>')
    sandbox = '' if define else f'''<section id="sandbox" aria-labelledby="sandbox-title"><div class="section-heading"><div>
<h2 id="sandbox-title">Does execution stay inside its boundary?</h2>
<p>Access, resource settings and cleanup are checked separately from repair quality.</p></div></div>
{render_probes(reports, link_root)}
<p class="meta">Trusted dummy probes, with no AI involved. Observations apply to this image and environment; they aren't a general security proof. Kernel limits were read, not stress-tested.</p>
<details class="history"><summary>Runtime readiness and setup history</summary>
<p>These checks only ask Podman which features are available. They don't run repair code.</p>
{render_preflights(reports, link_root)}</details></section>'''
    guide = {key: value for key, value in FILE_GUIDE.items() if not define or key not in (
        "tools/harness/sandbox.py", "tools/harness/container.py", "tools/harness/probes.py",
        "tools/harness/pi_run.py", "tools/harness/pi/config.local.json", "context.json / pi-session.json",
        "reports/harness/sandbox-preflight/runs/<run-id>/")}
    glossary = {key: value for key, value in GLOSSARY.items() if not define or key not in ("Podman", "Sandbox readiness")}
    intro = ('A known bug and prepared repairs test whether our checker reports the right answer. '
             'These checks ran on the host, without AI or containers.' if define else
             'Prepared repairs test our checker and tools. Sandbox probes test the execution boundary. Pi attempts below record model settings and whether inference could start.')
    takeaway = ('Known working and ineffective repairs test the checker. A focused test passing and human approval are separate results.'
                if define else 'A repair passing its test, a sandbox respecting its limits, and your approval are three separate results.')
    return f'''<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{escape(progress['stage'])} / Harness review</title><style>
:root {{ color-scheme: light dark; font-family: Ubuntu, "Segoe UI", sans-serif; font-size: 16px;
--report-background: #f2f5f8; --report-foreground: #182f46; --report-muted: #526478;
--report-paper: #fff; --report-panel: #eaf2f6; --report-line: #d4dde5; --report-link: #075e75;
--report-pass: #216342; --report-pass-bg: #e7f3eb; --report-attention: #805008; --report-attention-bg: #fff0d6;
background: var(--report-background); color: var(--report-foreground); }}
* {{ box-sizing: border-box; }} html {{ scroll-padding-top: 24px; }}
body {{ margin: 0; line-height: 1.55; background: var(--report-background); color: var(--report-foreground); }}
.shell {{ max-width: 1240px; margin: auto; padding: 36px; }}
.masthead, .run-heading, .section-heading {{ display: flex; align-items: baseline; justify-content: space-between; gap: 16px; }}
.masthead {{ border-bottom: 1px solid var(--report-line); padding-bottom: 16px; font-size: .9rem; }}
.masthead strong {{ font-size: 1.1rem; }} .masthead a {{ margin-left: 16px; }}
.saved-reports {{ display: flex; flex-wrap: wrap; gap: 16px; padding: 14px 0 0; font-size: .85rem; }}
.intro {{ padding: 34px 0 22px; max-width: 74ch; }}
h1 {{ font-size: clamp(2rem, 4vw, 3rem); letter-spacing: -.035em; line-height: 1.12; margin: 14px 0; }}
h2 {{ font-size: 1.4rem; letter-spacing: -.02em; margin: 0; line-height: 1.3; }}
h3 {{ font-size: 1.03rem; margin: 0; line-height: 1.45; }} p {{ margin: 10px 0; max-width: 70ch; }}
a {{ color: var(--report-link); text-underline-offset: 3px; }}
:focus-visible {{ outline: 3px solid var(--report-link); outline-offset: 4px; }}
.skip {{ position: absolute; left: -1000px; }} .skip:focus {{ left: 16px; top: 12px; background: var(--report-paper); padding: 8px; }}
.meta, .stages span {{ color: var(--report-muted); font-size: .82rem; }}
.badge {{ display: inline-block; flex-shrink: 0; border-radius: 4px; padding: 3px 9px; font-size: .76rem; font-weight: 650; }}
.pass {{ color: var(--report-pass); }} .badge.pass {{ background: var(--report-pass-bg); }}
.attention {{ color: var(--report-attention); }} .badge.attention {{ background: var(--report-attention-bg); }}
.review-state {{ background: var(--report-panel); color: var(--report-link); }}
.stages {{ display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 12px; list-style: none; padding: 0; margin: 0 0 30px; }}
.stages li {{ border-top: 3px solid var(--report-line); padding-top: 8px; font-size: .88rem; }}
.stages li[aria-current="step"] {{ border-color: var(--report-link); }} .stages span {{ display: block; margin-top: 3px; }}
.layout {{ display: grid; grid-template-columns: minmax(0, 1fr) 270px; gap: 36px; align-items: start; }}
main, aside {{ min-width: 0; }} main > section {{ margin-bottom: 32px; scroll-margin-top: 24px; }}
.section-heading {{ margin-bottom: 16px; }} .section-heading p {{ margin-bottom: 0; }}
.section-heading > span {{ color: var(--report-muted); white-space: nowrap; font-size: .8rem; }}
.run {{ background: var(--report-paper); border: 1px solid var(--report-line); border-radius: 8px; padding: 20px; margin: 12px 0; }}
.run p {{ font-size: .9rem; }} .run .meta {{ font-size: .77rem; }}
.check-steps {{ display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); list-style: none; padding: 0; margin: 18px 0; gap: 12px; }}
.check-steps li {{ border-top: 3px solid currentColor; padding-top: 8px; min-width: 0; }}
.check-steps span {{ color: var(--report-muted); font-size: .75rem; display: block; }}
.check-steps strong {{ font-size: .86rem; display: block; margin-top: 5px; }}
details {{ margin-top: 12px; font-size: .85rem; }} .run details p {{ font-size: inherit; }}
summary {{ cursor: pointer; color: var(--report-link); font-size: .88rem; padding: 5px 0; }}
details[open] > summary {{ margin-bottom: 12px; }} .history {{ border-top: 1px solid var(--report-line); padding-top: 8px; margin-top: 20px; }}
dt {{ font-weight: 650; margin-top: 16px; }} dd {{ margin: 4px 0 12px; color: var(--report-muted); }}
code {{ overflow-wrap: anywhere; font-size: .85em; }} ul, ol {{ padding-left: 22px; }} li {{ margin-bottom: 8px; }}
output {{ font-family: ui-monospace, "SFMono-Regular", Consolas, monospace; font-variant-numeric: tabular-nums; }}
aside {{ border-left: 1px solid var(--report-line); padding-left: 24px; position: sticky; top: 24px; font-size: .88rem; }}
aside section {{ margin-bottom: 28px; }} aside h2 {{ font-size: 1rem; }} aside p, aside li {{ color: var(--report-muted); }}
.review-links {{ padding-left: 20px; }} .review-links a {{ display: block; padding: 3px 0; }}
.lesson {{ border-left: 3px solid var(--report-link); padding-left: 16px; margin: 20px 0; }}
.empty {{ padding: 20px; background: var(--report-panel); }}
footer {{ border-top: 1px solid var(--report-line); padding-top: 18px; margin-top: 32px; }} footer p {{ max-width: 100ch; }}
.evidence-viewer {{ position: fixed; inset: 0 0 0 auto; margin: 0; width: min(800px, 100%); height: 100dvh;
max-width: 100%; max-height: 100dvh; padding: 0; border: 0; border-left: 1px solid var(--report-line);
background: var(--report-paper); color: var(--report-foreground); box-shadow: -12px 0 48px #0003; }}
.evidence-viewer[open] {{ display: flex; flex-direction: column; }} .evidence-viewer::backdrop {{ background: #101e2d80; }}
body.evidence-open {{ overflow: hidden; }}
.evidence-header {{ padding: 24px; border-bottom: 1px solid var(--report-line); }}
.evidence-header .run-heading {{ align-items: flex-start; }} .evidence-header h2 {{ font-size: 1.3rem; }}
.evidence-header p {{ font-size: .85rem; }} .evidence-header .meta {{ overflow-wrap: anywhere; }}
.evidence-tools {{ display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; margin-top: 16px; }}
.evidence-viewer button, .evidence-viewer select {{ font: inherit; color: var(--report-foreground);
background: var(--report-panel); border: 1px solid var(--report-line); border-radius: 4px; padding: 7px 10px; }}
.evidence-viewer button {{ cursor: pointer; }} .evidence-viewer select {{ max-width: 100%; }}
#evidence-content {{ overflow: auto; margin: 0; padding: 24px; flex: 1; min-height: 0;
background: var(--report-paper); color: var(--report-foreground);
white-space: pre-wrap; overflow-wrap: anywhere; font: .82rem/1.65 ui-monospace, monospace; tab-size: 4; }}
#evidence-status {{ max-width: none; padding: 10px 24px; margin: 0; background: var(--report-panel); font-size: .8rem; }}
.patch-add {{ color: var(--report-pass); background: var(--report-pass-bg); }}
.patch-remove {{ color: var(--report-attention); background: var(--report-attention-bg); }}
@media (max-width: 960px) {{ .shell {{ padding: 24px; }} .layout {{ grid-template-columns: minmax(0, 1fr) 230px; gap: 24px; }} }}
@media (max-width: 760px) {{ .layout {{ display: flex; flex-direction: column; }} main, aside {{ width: 100%; }}
aside {{ position: static; border-left: 0; border-top: 1px solid var(--report-line); padding: 24px 0 0; }}
.shell {{ padding: 20px; }} .stages {{ grid-template-columns: repeat(3, minmax(0, 1fr)); }}
.run-heading {{ align-items: flex-start; }} .masthead {{ flex-wrap: wrap; gap: 8px; }}
.masthead a {{ margin: 0 16px 0 0; }} .section-heading {{ align-items: flex-start; }} .run {{ padding: 16px; }} }}
@media (max-width: 420px) {{ .check-steps {{ grid-template-columns: 1fr; gap: 8px; }}
.check-steps li {{ border-top: 0; border-left: 3px solid currentColor; padding: 2px 0 2px 12px; margin: 0; }}
.check-steps strong {{ margin: 0; }} .run-heading {{ flex-wrap: wrap; gap: 6px; }} }}
@media (prefers-color-scheme: dark) {{ :root {{ --report-background: #18242f; --report-foreground: #e2ebf2;
--report-muted: #b4c3cf; --report-paper: #21313f; --report-panel: #263f50; --report-line: #415568;
--report-link: #96d2e4; --report-pass: #a1ddb9; --report-pass-bg: #244535;
--report-attention: #f4cd85; --report-attention-bg: #493b24; }} }}
@media print {{ :root {{ color-scheme: light; }} .shell {{ padding: 0; }} aside {{ position: static; }} .run {{ break-inside: avoid; }} }}
</style></head><body><div class="shell">
<a class="skip" href="#results">Skip to results</a>
<header class="masthead"><strong>Mapbox TUI / Developer harness</strong><nav aria-label="Report sections">
<a href="#results">Results</a>{'<a href="#sandbox">Sandbox</a>' if not define else ''}{'<a href="#pi">Pi tools</a>' if pi_section else ''}<a href="#review">Review guide</a><a href="#reference">Reference</a></nav></header>
{saved_nav}
<div class="intro"><span class="badge review-state">{escape(progress["stage"])}: {escape(STATE_LABELS[progress["status"]])}</span>
<h1>{escape(progress['stage'])}: {'repair calibration' if define else 'controlled test execution'}</h1>
<p>{escape(intro)}</p>
<p class="meta">{'Historical checkpoint: the labels below retain the status recorded at the time.' if snapshot and snapshot.get('restored') else 'Saved local stage report.'}
Decisions recorded {escape(progress["updated_at"])}.</p>{provenance}</div>
<ol class="stages">{milestones}</ol>
<div class="layout"><main>
<section id="results" aria-labelledby="results-title">
<div class="section-heading"><div><h2 id="results-title">Does the checker spot a real repair?</h2>
<p>Compare the latest good and wrong examples{' requested in containers' if container_mode else ''}. A failure in the middle step is expected.</p></div></div>
{records}
<details id="experiment-guide"><summary>How to read the three steps</summary>
<ol><li><strong>Original code:</strong> the import test should pass.</li>
<li><strong>Deliberately broken code:</strong> remove handling for a UTF-8 marker at the start of a file. The same test should fail.</li>
<li><strong>After the repair:</strong> a working repair should pass; an ineffective one should still fail.</li></ol>
<p>The test checks that a valid marker leaves imported data unchanged, while misplaced markers remain invalid.
Setup problems such as missing tools or dependencies mean we couldn't judge the repair.</p></details>
</section>
{sandbox}
{pi_section}
<section id="reference" aria-labelledby="reference-title"><h2 id="reference-title">Reference, when you need it</h2>
<details id="glossary"><summary>Words explained</summary><dl>{definitions(glossary)}</dl></details>
<details id="file-guide"><summary>Files and evidence explained</summary>
<p><code>tools/harness/</code> contains scripts. <code>reports/harness/</code> contains local, Git-ignored evidence.
Receipt links open the evidence drawer. Select the result, task, events, patches or Pi context/conversation without leaving this page.</p><dl>{definitions(guide)}</dl></details>
<details><summary>The roadmap and recorded decisions</summary><ol>
<li><strong>Define:</strong> agree the task and prove that our checking process can spot a known bug and repair.</li>
<li><strong>Connect:</strong> let Pi, the AI runtime, investigate and propose a repair.</li>
<li><strong>Verify:</strong> independently check the exact change and run the wider required tests.</li>
<li><strong>Recover:</strong> handle stopped runs and unfinished changes.</li>
<li><strong>Compare:</strong> measure which changes to the harness actually help.</li>
<li><strong>Use:</strong> try real tasks under human supervision, starting with imports.</li>
</ol><ul>{items(progress["decisions"])}</ul>
<p>Obstacles: {escape('; '.join(progress['blockers']) or 'none recorded')}.</p></details>
<details><summary>Is the AI getting better at repairs?</summary><p>No comparisons yet. Prepared examples test the harness; one live attempt cannot establish a trend.
Later we'll compare repeated AI attempts on the same tasks and settings. These timings alone don't show improved repair ability.</p></details>
</section></main>
<aside id="review" aria-label="Review guide"><section><h2>Your review path</h2>
<ol class="review-links"><li><a href="#results">Compare good and wrong</a>Explain why their final steps differ.</li>
{('<li><a href="#experiment-guide">Review setup failures</a>Missing dependencies and timeouts must remain separate from failed repairs.</li>' if define else '<li><a href="#sandbox">Inspect the boundary checks</a>Look for access denial and confirmed cleanup.</li>')}
<li><a href="#takeaway">Discuss the takeaway</a>Then decide whether to move on.</li></ol></section>
<section id="takeaway"><h2>What to take away</h2>
<div class="lesson"><p>{escape(takeaway)}</p></div>
<p>{'Connect is the next stage: demonstrate controlled execution before connecting Pi. Container isolation was not implemented in Define.' if define else 'Pi chooses investigation and repair steps. Our runner enforces the permitted files, execution limits and required checks.'}</p>
<details><summary>Questions for our discussion</summary><ul>{items(progress["questions"])}</ul></details></section>
<section><h2>Before the next step</h2><p>{escape(STATE_LABELS[progress["status"]])}. Passing tests never mark this reviewed automatically.</p>
<details><summary>Next scope and checkpoint notes</summary><p>{escape(progress["next"])}</p></details></section></aside></div>
<footer><p class="meta">Generated {escape(now())}. Source: {escape(progress["source"])}.
{'Local receipts contain the original host calibration experiments.' if define else 'Local receipts include repair, runtime-readiness and sandbox-probe records.'}</p>
<p><a href="https://github.com/adamcbrewer/mapbox-tui/issues/103">Define scope #103</a>
and <a href="https://github.com/adamcbrewer/mapbox-tui/issues/104">Connect scope #104</a>
and <a href="https://github.com/adamcbrewer/mapbox-tui/issues/111">measurement plan #111</a>.</p>
</footer></div>
<dialog id="evidence-viewer" class="evidence-viewer" aria-labelledby="evidence-title" aria-describedby="evidence-description">
<div class="evidence-header"><div class="run-heading"><h2 id="evidence-title">Run evidence</h2>
<button type="button" id="evidence-close" aria-label="Close evidence">Close</button></div>
<p id="evidence-run" class="meta"></p><p id="evidence-description"></p>
<div class="evidence-tools"><label for="evidence-file">File</label><select id="evidence-file">
<option value="result.json">Result</option><option value="task.json">Task</option><option value="events.jsonl">Events</option>
<option value="candidate.patch">Candidate patch</option><option value="seed.patch">Seed patch</option>
<option value="context.json">Context</option><option value="pi-session.json">Pi conversation</option></select>
<a id="evidence-original">Open original file</a></div></div>
<p id="evidence-status" role="status"></p><pre id="evidence-content" role="region" tabindex="0" aria-label="Evidence file contents"></pre>
</dialog>
<script type="application/json" id="evidence-data">{embedded_evidence(reports, link_root)}</script>
<script id="evidence-script">{(HERE / 'evidence.js').read_text()}</script>
</body></html>'''


def evidence_inventory(source):
    inventory = {}

    def visit(path):
        relative = path.relative_to(source).as_posix()
        if path.is_symlink():
            inventory[relative] = {"kind": "symlink", "target": os.readlink(path)}
        elif path.is_dir():
            for child in path.iterdir():
                visit(child)
        elif path.is_file():
            with path.open("rb") as content:
                inventory[relative] = {"kind": "file", "sha256": hashlib.file_digest(content, "sha256").hexdigest()}
        else:
            raise ValueError(f"Unsupported saved evidence entry: {relative}")

    if source.is_symlink():
        raise ValueError("Saved evidence directory must not be a symlink")
    for path in source.iterdir():
        if path.name not in ("progress.json", "snapshot.json"):
            visit(path)
    return inventory


def receipt_hashes(source, receipts):
    hashes = {}
    for path in receipts:
        relative = path.relative_to(source)
        current = source
        for part in relative.parts:
            if current.is_symlink():
                raise ValueError(f"Receipt path must not contain symlinks: {relative}")
            current = current / part
        if path.is_symlink():
            raise ValueError(f"Receipt must not be a symlink: {relative}")
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(descriptor, "rb") as content:
            if not stat.S_ISREG(os.fstat(content.fileno()).st_mode):
                raise ValueError(f"Receipt must be a regular file: {relative}")
            hashes[relative.as_posix()] = hashlib.file_digest(content, "sha256").hexdigest()
    return hashes


def capture_stage(destination, progress, source, receipts, source_ref, restored):
    destination.mkdir(parents=True, exist_ok=True)
    for path in receipts:
        relative = path.parent.relative_to(source)
        shutil.copytree(path.parent, destination / relative, symlinks=True, dirs_exist_ok=True)
    write_json(destination / "progress.json", progress)
    saved_receipts = [path for folder in RUN_FOLDERS
                      for path in sorted((destination / folder).glob("*/result.json"))]
    snapshot = {"schema_version": 1, "stage": progress["stage"], "captured_at": now(),
                "source_ref": source_ref, "restored": restored,
                "evidence_source": str(source.resolve()),
                "progress_sha256": digest((destination / "progress.json").read_bytes()),
                "receipt_sha256": receipt_hashes(destination, saved_receipts),
                "evidence_inventory": evidence_inventory(destination)}
    write_json(destination / "snapshot.json", snapshot)


def save_stage(reports, progress, source, *, source_ref, restored=False):
    if progress["stage"] not in STAGES or progress["status"] not in STATES:
        raise ValueError("Unknown progress stage or status")
    name = progress["stage"].lower()
    destination = reports / "snapshots" / name
    if destination.is_symlink():
        raise ValueError("Saved evidence directory must not be a symlink")
    if restored and destination.exists():
        raise ValueError(f"The {name} snapshot already exists; use --refresh-saved to render it again")
    receipts = [path for folder in RUN_FOLDERS
                for path in sorted((source / folder).glob("*/result.json"))]
    if restored and not receipts:
        raise ValueError("Restoration needs original evidence; no saved receipts were found")
    receipt_hashes(source, receipts)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=f".{name}-", dir=destination.parent) as temporary:
        staging = Path(temporary)
        if destination.exists():
            shutil.copytree(destination, staging, symlinks=True, dirs_exist_ok=True)
        capture_stage(staging, progress, source, receipts, source_ref, restored)
        publish_snapshot(staging, destination)
    return name


def publish_snapshot(staging, destination):
    if not destination.exists():
        staging.rename(destination)
        return
    # Linux exchange replaces populated directories without exposing a missing or partial snapshot.
    exchange = getattr(ctypes.CDLL(None, use_errno=True), "renameat2", None)
    if exchange is None:
        raise OSError("Atomic snapshot replacement requires Linux renameat2 support")
    exchange.argtypes = (ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint)
    exchange.restype = ctypes.c_int
    if exchange(-100, os.fsencode(staging), -100, os.fsencode(destination), 2):
        code = ctypes.get_errno()
        raise OSError(code, f"Cannot atomically replace saved snapshot: {os.strerror(code)}")


def render_saved(reports, name):
    source = reports / "snapshots" / name
    progress_bytes = (source / "progress.json").read_bytes()
    snapshot = json.loads((source / "snapshot.json").read_text())
    if digest(progress_bytes) != snapshot["progress_sha256"]:
        raise ValueError(f"Saved {name} progress does not match its snapshot hash")
    receipts = receipt_hashes(source, [path for folder in RUN_FOLDERS
                                     for path in (source / folder).glob("*/result.json")])
    if receipts.keys() != snapshot["receipt_sha256"].keys():
        raise ValueError(f"Saved {name} receipt inventory does not match its snapshot")
    for relative, hashed in receipts.items():
        if hashed != snapshot["receipt_sha256"][relative]:
            raise ValueError(f"Saved {name} receipt does not match its snapshot hash: {relative}")
    if not isinstance(snapshot.get("evidence_inventory"), dict):
        raise ValueError(f"Saved {name} snapshot has no complete evidence inventory; verify original evidence before recapturing")
    if evidence_inventory(source) != snapshot["evidence_inventory"]:
        raise ValueError(f"Saved {name} evidence does not match its snapshot inventory")
    progress = json.loads(progress_bytes)
    target = reports / f"{name}.html"
    with tempfile.TemporaryDirectory(prefix=f".{name}-render-", dir=reports) as temporary:
        staged = Path(temporary) / target.name
        staged.write_text(render(source, progress, link_root=reports, snapshot=snapshot))
        if evidence_inventory(source) != snapshot["evidence_inventory"]:
            raise ValueError(f"Saved {name} evidence changed during rendering")
        staged.replace(target)
    return target


def git_output(*args):
    return subprocess.run(["git", *args], cwd=ROOT, check=True, capture_output=True,
                          text=True, timeout=10).stdout.strip()


def main():
    parser = argparse.ArgumentParser(description="Save a stage report with its evidence, or restore a historical stage.")
    parser.add_argument("--restore-from", metavar="COMMIT", help="Read historical progress from this Git commit")
    parser.add_argument("--evidence", type=Path, help="Original report directory; required with --restore-from")
    parser.add_argument("--refresh-saved", action="store_true", help="Apply the current design to saved snapshots without recapturing evidence")
    args = parser.parse_args()
    if bool(args.restore_from) != bool(args.evidence) or (args.refresh_saved and args.restore_from):
        parser.error("Use --restore-from and --evidence together, separately from --refresh-saved")
    try:
        progress = json.loads((HERE / "progress.json").read_text())
        REPORTS.mkdir(parents=True, exist_ok=True)
        if args.refresh_saved:
            for stage in STAGES:
                if (REPORTS / "snapshots" / stage.lower() / "snapshot.json").is_file():
                    print(render_saved(REPORTS, stage.lower()))
        else:
            if args.restore_from:
                reference = git_output("rev-parse", "--verify", "--end-of-options", args.restore_from + "^{commit}")
                saved_progress = json.loads(git_output("show", reference + ":tools/harness/progress.json"))
                name = save_stage(REPORTS, saved_progress, args.evidence.resolve(), source_ref=reference, restored=True)
            else:
                name = save_stage(REPORTS, progress, REPORTS, source_ref=git_output("rev-parse", "HEAD"))
            print(render_saved(REPORTS, name))
        current = REPORTS / (progress["stage"].lower() + ".html")
        if current.is_file():
            target = REPORTS / "index.html"
            with tempfile.TemporaryDirectory(prefix=".index-render-", dir=REPORTS) as temporary:
                staged = Path(temporary) / target.name
                staged.write_bytes(current.read_bytes())
                staged.replace(target)
            print(target)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.error(f"Could not save the report: {error}")


if __name__ == "__main__":
    main()
