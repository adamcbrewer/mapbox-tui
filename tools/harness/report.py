import html
import json

from run import HERE, REPORTS, now

STAGES = ("Define", "Connect", "Verify", "Recover", "Compare", "Use")
STATES = ("planned", "active", "blocked", "awaiting-review", "reviewed")


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
    "repair_failed": "The proposed repair did not pass this check.",
    "calibration_failed": "The setup did not produce the expected working and broken examples.",
    "blocked": "We couldn’t complete the checks. This does not tell us whether the repair works.",
    "invalid_input": "The request could not be used. Check the patch file and which changes are allowed.",
    "timeout": "The run reached its time limit before finishing.",
    "cancelled": "The run was stopped before finishing.",
    "incomplete": "No final result was recorded. The run may still be working or may have stopped unexpectedly.",
}
GLOSSARY = {
    "Harness": "The software around the AI that controls the task, tools, checks and saved evidence.",
    "Model / agent / Pi": "The model is the AI. An agent uses a model and tools to attempt a task. Pi will provide that tool-using loop later; it is not connected here yet.",
    "Checker / verifier": "Code that runs required checks and records their results. It does not understand or approve the code like a person.",
    "Calibration": "Trying examples with known answers to see whether our checking process reports them correctly.",
    "Baseline": "The starting code used for comparison. Here it works; a real repair task may start with broken code.",
    "Seed patch": "A change that deliberately plants a bug in the temporary copy.",
    "Candidate patch": "The proposed repair we are testing. In these demos it was prepared in advance, not written by AI.",
    "Patch": "A file listing code lines to add or remove.",
    "Fixture": "Prepared example input used by a test, rather than your personal activity data.",
    "Run / attempt / check": "A run is one execution of a task. An attempt is one proposed solution within it. A check is an individual test or validation step. This pilot tries one solution per run.",
    "Run ID / hash": "A run ID labels its folder; the short ID beside Receipt helps you find it. A hash is a fingerprint identifying exact file contents. They are different things.",
    "Receipt": "The saved result summary and references to its evidence. It is not human approval.",
    "Fault example": "A deliberately introduced setup problem or slow command. It tests how the runner handles trouble, not whether a repair works.",
    "Podman": "The container tool chosen for a disposable test environment in Connect (#104). It is not used by this pilot yet.",
}
FILE_GUIDE = {
    "tools/harness/run.py": "Conducts the experiment and saves its evidence.",
    "tools/harness/task.json": "The instruction sheet: starting code, expected behaviour, allowed changes and required test.",
    "tools/harness/fixtures/*.patch": "The deliberate bug and prepared example repairs.",
    "tools/harness/report.py": "Builds this HTML page from saved records. It does not run tests.",
    "tools/harness/progress.json": "Records our stage, decisions and pending discussion. Test results do not update our approval.",
    "tools/harness/test_harness.py": "Tests the Python machinery itself.",
    "tools/harness/README.md": "Commands and instructions for using and reviewing this pilot.",
    "reports/harness/index.html": "This report. Open it first; rebuild it after new runs.",
    "reports/harness/runs/<run-id>/": "Evidence from one experiment. Within it, task.json preserves the instructions, seed.patch preserves the deliberately broken change, and candidate.patch preserves the proposed repair.",
    "result.json (inside a run folder)": "The final summary: outcome, timings and exactly what was checked. Receipt links open this file.",
    "events.jsonl (inside a run folder)": "A chronological diary, with one event per line: a command started, finished, or a test result was recorded.",
    "*.stdout / *.stderr (inside a run folder)": "Command output and diagnostics. Usually only needed to investigate a problem. baseline and seed refer to the original and deliberately broken code; candidate refers to the proposed repair. scope checks allowed files; metadata checks file operations such as renaming or deleting; apply records applying a patch. go-version records the compiler version.",
    "snapshot.stdout": "An exception to the readable logs: this contains the archived source copy. snapshot.stderr contains any archive-command diagnostics.",
    "reports/harness/*.log": "Checks run on the harness implementation, separate from the individual experiments.",
}


def definitions(entries):
    return "".join(f"<dt>{escape(term)}</dt><dd>{escape(meaning)}</dd>" for term, meaning in entries.items())


def render_run(path, reports, run):
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
    if known and run.get("outcome") == "calibration_passed":
        stages = [check for check in recorded_checks if check.get("id") in ("baseline", "seed", "candidate")]
        known = ([check.get("id") for check in stages] == ["baseline", "seed", "candidate"] and
                 [check.get("outcome") for check in stages] == ["passed", "assertion_failed", "passed"] and
                 stages[0].get("exit_code") == 0 and stages[2].get("exit_code") == 0 and
                 isinstance(stages[1].get("exit_code"), int) and stages[1]["exit_code"] != 0)
    outcome = run.get("outcome") if known else None
    result = OUTCOMES.get(str(outcome), "Unreadable record or unknown result. Inspect the receipt before drawing a conclusion.")
    if candidate in ("empty", "wrong") and outcome == "repair_failed":
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
            checks.append(f'<li>{names[check["id"]]}: {escape(answers.get(check.get("outcome"), "unknown result"))}.</li>')
    duration = run.get("duration_seconds")
    elapsed = f"{duration:.2f} seconds" if isinstance(duration, (int, float)) else "Time not recorded"
    link = escape(path.relative_to(reports).as_posix())
    run_id = escape(path.parent.name)
    return f'''<article class="run">
<h3>{escape(title)}</h3>
<p><strong>What happened:</strong> {escape(result)}</p>
<p><strong>What we tried:</strong> {escape(tried)} <strong>Expected:</strong> {escape(expected)}</p>
<p><strong>Why this example matters:</strong> {escape(purpose)}</p>
<details><summary>See the steps and technical details</summary>
{('<ul>' + ''.join(checks) + '</ul>') if checks else '<p>No import test results recorded.</p>'}
<p>Saved status: <code>{escape(run.get('outcome', 'unknown'))}</code>.
Diagnostic: {escape(run.get('reason', 'No final explanation recorded'))}</p>
<p>Candidate label in the saved record: {escape(candidate if candidate is not None else 'not loaded')}.</p>
<p>Requested setup fault: {escape(run.get('fault', 'none'))}.
Fault introduced: {escape(run.get('fault_injected', 'not recorded'))}.</p>
<p>Started: {escape(run.get('started_at', 'not recorded'))}. Human approval is separate from these checks.</p></details>
<p class="meta">{escape(elapsed)}. <a href="{link}">Receipt</a>
<code title="Run ID: {run_id}">{escape(path.parent.name[:8])}</code></p>
</article>'''


def render(reports, progress):
    if progress["status"] not in STATES or progress["stage"] not in STAGES:
        raise ValueError("Unknown progress stage or status")
    milestones = "".join(
        f'<li><strong>{stage}</strong><span>{escape(STATE_LABELS[progress["status"]]) if stage == progress["stage"] else "Later"}</span></li>'
        for stage in STAGES
    )
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
    for path, run in sorted(records, key=lambda item: str(item[1].get("started_at", ""))):
        rows.append(render_run(path, reports, run))
    records = "".join(rows) if rows else (
        '<p class="empty">No runs recorded. Try the prepared working repair, then rebuild this page.</p>')
    items = lambda values: "".join(f"<li>{escape(value)}</li>" for value in values)
    return f'''<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Harness progress</title><style>
:root {{ color-scheme: light dark; font-family: system-ui, sans-serif;
--report-background: #f8fafc; --report-foreground: #203746; --report-muted: #536878;
--report-panel: #edf4fa; --report-link: #185e9e;
background: var(--report-background); color: var(--report-foreground); }}
body {{ margin: 0 auto; max-width: 1100px; background: var(--report-background); color: var(--report-foreground); }} main {{ padding: 24px; }}
h3 {{ font-size: 1.05rem; }} .run {{ border-top: 1px solid #8294a4; padding: 12px 0; }}
.run h3 {{ margin: 8px 0; }} .run p {{ margin: 8px 0; max-width: 80ch; }}
details {{ margin: 12px 0; }} summary {{ cursor: pointer; color: var(--report-link); }}
summary:focus-visible {{ outline: 2px solid currentColor; }} dt {{ font-weight: 650; margin-top: 12px; }} dd {{ margin: 4px 0 12px; line-height: 1.5; }}
code {{ overflow-wrap: anywhere; }}
h1 {{ font-size: 2rem; margin: 0 0 8px; }} h2 {{ font-size: 1.15rem; margin-top: 28px; }}
p, li {{ line-height: 1.55; }} small, .meta {{ color: var(--report-muted); font-size: .85rem; }}
.stages {{ display: grid; grid-template-columns: repeat(6, 1fr); gap: 8px; list-style: none; padding: 0; }}
.stages li {{ border-top: 4px solid #8294a4; padding-top: 10px; }}
.stages li:first-child {{ border-color: #236fba; }} .stages span {{ display: block; font-size: .8rem; }}
.review {{ border-left: 4px solid #236fba; padding: 1px 20px; background: var(--report-panel); }}
ul {{ padding-left: 22px; }} a {{ color: var(--report-link); }} a:focus-visible {{ outline: 2px solid currentColor; }}
.empty {{ padding: 16px; background: var(--report-panel); }}
@media (max-width: 640px) {{ main {{ padding: 16px; }} .stages {{ grid-template-columns: repeat(3, 1fr); }} }}
@media (prefers-color-scheme: dark) {{ :root {{ --report-background: #15232f; --report-foreground: #e2edf5;
--report-muted: #b0c5d5; --report-panel: #233c50; --report-link: #a9d0ff; }} }}
</style></head><body><main>
<h1>Can we tell whether a repair works?</h1>
<p>We’re building a tool that will let an AI try fixing a bug, check its work and bring you the change to review.</p>
<section class="review"><h2>Right now: test the checking process</h2>
<p><strong>No AI is involved in these examples.</strong> A Python script tries prepared changes with known answers.
We need to know it reports those answers correctly before asking an AI to supply a repair.</p>
<p>The script copies a known version of mapbox-tui into a temporary folder. Your working source files stay untouched.</p></section>
<p class="meta">Saved local report, generated {escape(now())}. Rebuild it to include new runs.</p>
<ol class="stages">{milestones}</ol>
<details><summary>How this fits into the bigger plan</summary><ol>
<li><strong>Define, our current stage:</strong> agree the task and prove that our checking process can spot a known bug and repair.</li>
<li><strong>Connect:</strong> let Pi, the AI runtime, investigate and propose a repair.</li>
<li><strong>Verify:</strong> independently check the exact change and run the wider required tests.</li>
<li><strong>Recover:</strong> handle stopped runs and unfinished changes.</li>
<li><strong>Compare:</strong> measure which changes to the harness actually help.</li>
<li><strong>Use:</strong> try real tasks under human supervision, starting with imports.</li>
</ol></details>
<h2>What happens in an experiment?</h2>
<ol><li><strong>Test working code.</strong> The original import test should pass.</li>
<li><strong>Deliberately break it.</strong> Remove handling for a marker some files have at their start. The same test should fail.</li>
<li><strong>Try the proposed repair.</strong> Apply the prepared change and run the test again. A working repair should pass; an ineffective one should fail.</li></ol>
<p>The test checks that a valid UTF-8 marker leaves imported data unchanged, while misplaced markers remain invalid.
A failure in step 2 is expected: it shows the test notices the bug we planted.</p>
<p>Setup-failure examples deliberately interrupt this process with a missing file, missing dependencies or a slow command.
They may stop before any import test runs. An unfinished check tells us nothing about whether the repair works.</p>
<h2>What did each experiment show?</h2>
<p>Each entry below is one run. A failed repair can be the expected answer in these demonstrations.
A missing tool, source file or dependency means we couldn’t check the repair. A time limit means the run stopped unfinished.</p>
{records}
<h2>What should I review?</h2>
<p>{escape(progress["next"])}</p>
<p><strong>Our discussion:</strong> {escape(STATE_LABELS[progress["status"]])}. Passing tests never mark this reviewed automatically.</p>
<ul>{items(progress["questions"])}</ul>
<p>Start with the working-repair and ineffective-repair examples above. You should be able to explain why their results differ without opening a JSON file.
“Receipt” opens the saved technical details if you want to investigate further.</p>
<p><strong>Takeaway:</strong> we are testing whether this checking process can distinguish a working repair from an ineffective one.
Passing this focused check does not prove the whole change is correct, safe or ready to merge.</p>
<details><summary>Decisions and remaining obstacles</summary><ul>{items(progress["decisions"])}</ul>
<p>Obstacles: {escape('; '.join(progress['blockers']) or 'none recorded')}.</p></details>
<h2>Words and files explained</h2>
<p>You normally only need this page. The guides below explain the details when you want to look further.</p>
<details id="glossary"><summary>What do these words mean?</summary><dl>
{definitions(GLOSSARY)}
</dl></details>
<details id="file-guide"><summary>Which file does what?</summary>
<p><code>tools/harness/</code> contains the scripts and instructions. <code>reports/harness/</code> contains their generated results, which stay local and Git-ignored.</p><dl>
{definitions(FILE_GUIDE)}
</dl></details>
<h2>Is the AI getting better at repairs?</h2>
<p>No comparisons yet. These are prepared experiments without an AI. Later we’ll compare repeated AI attempts on the same tasks and settings.
These timings alone don’t show improved repair ability.</p>
<p class="meta">Progress source: {escape(progress["source"])}. Decisions updated {escape(progress["updated_at"])}.
Run source: local runs/*/result.json. Raw records stay in reports/harness/.</p>
<p><a href="https://github.com/adamcbrewer/mapbox-tui/issues/103">Define scope #103</a>
and <a href="https://github.com/adamcbrewer/mapbox-tui/issues/111">measurement plan #111</a>.</p>
</main></body></html>'''


def main():
    progress = json.loads((HERE / "progress.json").read_text())
    REPORTS.mkdir(parents=True, exist_ok=True)
    target = REPORTS / "index.html"
    target.write_text(render(REPORTS, progress))
    print(target)


if __name__ == "__main__":
    main()
