import argparse
from collections import Counter, defaultdict
from datetime import datetime
import html
import json
import os
from pathlib import Path
import tempfile
from urllib.parse import quote

import evaluate
import measurements
import report
import report_visuals
from run import HERE, REPORTS, digest


def escape(value):
    return html.escape(str(value), quote=True)


def measurement(value, suffix=""):
    value = evaluate.number(value)
    return "Unknown" if value is None else f"{value:g}{suffix}"


def events(directory, run_id):
    path = directory / "events.jsonl"
    rows, problems, seen = [], [], {}
    try:
        if path.is_symlink():
            raise ValueError("Linked event journal")
        raw = path.read_bytes()
    except (OSError, ValueError) as error:
        return [], [f"Event journal unavailable: {error}"], None
    for line, text in enumerate(raw.splitlines(), 1):
        try:
            event = json.loads(text)
            if not isinstance(event, dict) or event.get("schema_version") != 1 or event.get("run_id") != run_id or not isinstance(event.get("kind"), str):
                raise ValueError("Event identity or kind does not match this run")
            identity = event.get("event_id")
            if not isinstance(identity, str) or not identity:
                raise ValueError("Event ID missing")
            if identity in seen:
                if seen[identity] != event:
                    raise ValueError("Conflicting duplicate event ID")
                continue
            seen[identity] = event
            rows.append(event)
        except (ValueError, TypeError) as error:
            problems.append(f"Journal line {line}: {error}. Other readable events are retained.")
    return rows, problems, digest(raw)


def flag_record(row, problem):
    row["problem"] = '; '.join(filter(None, (row.get("problem"), problem)))
    row["unconfirmed_completion"] = row["unconfirmed_completion"] or row["verified_success"]
    row["verified_success"] = row["first_attempt_success"] = False


def load(reports, progress=None):
    value = evaluate.inventory(reports)
    for row in value["records"]:
        receipt_version = row.get("receipt_snapshot", {}).get("schema_version")
        if receipt_version not in (None, 1):
            flag_record(row, f"Unsupported receipt schema version: {receipt_version}")
        result = row.get("receipt_snapshot", {})
        shapes = {"model": (str, type(None)), "task": (str, type(None)), "workflow": (str,), "synthetic": (bool, type(None))}
        invalid = [field for field, types in shapes.items() if field in result and type(result[field]) not in types]
        if invalid:
            flag_record(row, "Malformed receipt metadata: " + ', '.join(invalid))
            row["missing_pins"] = [*row.get("missing_pins", []), *("invalid " + field for field in invalid)]
            if "workflow" in invalid:
                row["workflow"] = "unknown-workflow"
            if "synthetic" in invalid:
                row["synthetic"] = None
        if not isinstance(row["run_id"], str):
            row["run_id"] = Path(row["receipt"]).parent.name
            flag_record(row, "Invalid run ID in receipt; using the directory label for inspection.")
    for row in value["records"]:
        row.update(duplicate=False, conflicting_duplicate=False)
        directory = reports / Path(row["receipt"]).parent
        if evaluate.local_evidence(directory, reports):
            row["events"], row["event_problems"], row["events_sha256"] = events(directory, row["run_id"])
        else:
            row.update(events=[], event_problems=["Linked evidence needs inspection."], events_sha256=None)
    identities = defaultdict(list)
    for row in value["records"]:
        identities[row["run_id"]].append(row)
    for rows in identities.values():
        if len(rows) < 2:
            continue
        signatures = {(r.get("receipt_sha256"), r["events_sha256"], r.get("settings_sha256"),
                       (r.get("operator_evidence") or {}).get("sha256")) for r in rows}
        identical = len(signatures) == 1 and rows[0].get("receipt_sha256") is not None
        canonical = max(rows, key=lambda r: r["verified_success"]) if identical else None
        for row in rows:
            row.update(duplicate=row is not canonical, conflicting_duplicate=not identical)
    by_path = {row["receipt"]: row for row in value["records"]}
    for row in value["records"]:
        row["measurement"] = measurements.normalize(row, by_path, reports)
    value["comparisons"] = measurements.comparisons(reports, value["records"])
    if progress is not None:
        raw = progress.read_bytes()
        value["progress"] = json.loads(raw)
        value["progress_sha256"] = digest(raw)
        value["progress_source"] = str(progress)
    return value


def link(path, output, label):
    target = quote(os.path.relpath(path, output.parent), safe="/.")
    return f'<a href="{escape(target)}">{escape(label)}</a>'


def candidate_link(row, reports, output):
    expected = row.get("receipt_snapshot", {}).get("patch_sha256")
    path = reports / Path(row["receipt"]).parent / "candidate.patch"
    try:
        raw = measurements.read_local(path, reports)
        if not isinstance(expected, str) or digest(raw) != expected:
            raise ValueError("Candidate identity is missing or differs")
        return link(path, output, "Exact candidate patch")
    except (OSError, ValueError):
        return "Exact candidate unavailable"


def metric_overview(measured):
    metrics = measured["metrics"]
    return (f'Checked repair attempts: {measurement(metrics["repair_attempts"])}; retries: {measurement(metrics["retries"])}; '
            f'tokens across all model requests: {measurement(metrics["tokens"])}; '
            f'total cost: {measurement(metrics["total_cost_usd"], " USD")}; '
            f'operator corrections: {measurement(metrics["user_corrections"])}; '
            f'recorded attention: {measurement(metrics["attention_seconds"], " s")}. '
            f'Incorrect success claim: {"Unknown; no bound claim review" if measured["false_completion"] is None else "Found" if measured["false_completion"] else "None found in the recorded review"}. '
            f'Recovery observed: {"yes" if measured["recovery"]["observed"] else "not recorded"}; '
            f'recovery outcome: {escape(measured["recovery"]["outcome"] or "Unknown")}. '
            f'Cost basis/rate version: {escape(json.dumps(measured["cost_basis"]) if measured["cost_basis"] else "Not recorded; no estimate invented")}.')


def project_section(value, reports, output):
    progress = value.get("progress")
    if not progress:
        return '<h2 id="project">Where are we?</h2><p>Project progress not recorded. Tests cannot mark a learning checkpoint reviewed.</p>'
    rows = []
    for stage in report.STAGES:
        saved = None
        path = reports / "snapshots" / stage.lower() / "progress.json"
        if stage == progress["stage"]:
            saved = progress
        elif path.is_file():
            try:
                saved = json.loads(measurements.read_local(path, reports))
            except (OSError, ValueError):
                pass
        state = saved.get("status", "unknown") if isinstance(saved, dict) else "unknown"
        label = report.STATE_LABELS.get(state, "No recorded checkpoint state")
        evidence = reports / (stage.lower() + ".html")
        source = link(evidence, output, "Saved stage report") if evidence.is_file() and not evidence.is_symlink() else "Stage report unavailable"
        rows.append(f'<li><strong>{escape(stage)}</strong>: {escape(label)}. {source}. '
                    f'{"Current stage." if stage == progress["stage"] else "Historical state; not inferred from issue closure."}</li>')
    items = lambda values: ''.join(f'<li>{escape(v)}</li>' for v in values)
    return f'''<section aria-labelledby="project"><h2 id="project">Where are we?</h2>
<p>This report explains the harness around Pi: what is implemented, what each run did and what the measurements can establish.
Stage: {escape(progress['stage'])}. {escape(report.STATE_LABELS.get(progress['status'], progress['status']))}.
Last decision update: {escape(progress.get('updated_at', 'Unknown'))}.</p>
<p>Next deliverable: {escape(progress.get('next', 'Not recorded'))}</p>
<p>Source: {escape(progress.get('source', 'Not recorded'))}. Progress hash: <code>{escape(value['progress_sha256'])}</code>.</p>
<p><a href="https://github.com/adamcbrewer/mapbox-tui/issues/111">Source issue and learning checkpoint</a>.</p>
<details><summary>Roadmap and recorded checkpoint states</summary><ol>{''.join(rows)}</ol>
<p>States: planned, active, blocked, awaiting-review and reviewed. An earlier position on the roadmap does not establish approval.</p></details>
<details><summary>Recorded decisions ({len(progress.get('decisions', []))})</summary><ul>{items(progress.get('decisions', []))}</ul></details>
<p>Blockers: {escape('; '.join(progress.get('blockers', [])) or 'None recorded')}.</p>
<details open><summary>Questions waiting for review</summary><ul>{items(progress.get('questions', [])) or '<li>None recorded.</li>'}</ul></details></section>'''


METRIC_LABELS = {"duration_seconds": "Elapsed seconds", "repair_attempts": "Repair candidates checked", "retries": "Repair retries",
                 "tokens": "Total reported tokens", "model_cost_usd": "Model cost, USD", "execution_cost_usd": "Execution cost, USD",
                 "total_cost_usd": "Total cost, USD", "user_corrections": "Operator corrections", "attention_seconds": "Recorded attention, seconds",
                 "review_seconds": "Human review, seconds"}


def chart(rows, key, reports, output):
    ordered = sorted(rows, key=lambda r: str(r.get("started_at") or ""))
    values = [(r, r["measurement"][key] if key in ("first_attempt_success", "eventual_success") else r["measurement"]["metrics"][key]) for r in ordered]
    known = [(r, int(v) if type(v) is bool else v) for r, v in values if evaluate.number(v) is not None or type(v) is bool]
    label = {"first_attempt_success": "First-repair checked success", "eventual_success": "Eventual checked success"}.get(key, METRIC_LABELS.get(key, key))
    if not known:
        return f'<p>{escape(label)}: Unknown. No measurements recorded for {len(rows)} submitted tasks.</p>'
    maximum = max(v for _, v in known) or 1
    points, raw = [], []
    for position, (row, value) in enumerate(known):
        x = 35 + position * 570 / max(1, len(known) - 1)
        y = 140 - value * 110 / maximum
        receipt_url = quote(os.path.relpath(reports / row["receipt"], output.parent), safe="/.")
        points.append(f'<a href="{escape(receipt_url)}" aria-label="{escape(row["run_id"])}: {value:g}">'
                      f'<circle cx="{x:g}" cy="{y:g}" r="5"><title>{escape(row["run_id"])}: {value:g}</title></circle></a>')
        raw.append(f'<tr><th scope="row">{link(reports / row["receipt"], output, row["run_id"])}</th><td>{escape(row.get("started_at") or "Unknown")}</td>'
                   f'<td>{value:g}</td><td>{candidate_link(row, reports, output)}</td></tr>')
    return f'''<figure><figcaption>{escape(label)}: {len(known)} known, {len(rows) - len(known)} missing; {len(rows)} submitted tasks</figcaption>
<p>Ordered by recorded start time; unknown timestamps are listed first. Calendar observations are descriptive.</p>
<svg viewBox="0 0 640 180" role="group" aria-label="{escape(label)} in recorded calendar order; points link to source receipts">
<line x1="35" y1="140" x2="620" y2="140" stroke="currentColor"/><text x="0" y="144">0</text>
<text x="0" y="25">{maximum:g}</text>{''.join(points)}<text x="35" y="172">Earlier observations → later observations</text></svg>
<details><summary>Raw points, receipts and exact candidates</summary><div class="table-scroll"><table><thead><tr><th>Run</th><th>Started</th><th>Value</th><th>Candidate</th></tr></thead><tbody>{''.join(raw)}</tbody></table></div></details></figure>'''


def summary_section(rows):
    summary = measurements.summarize(rows)
    cells = []
    for key, label in METRIC_LABELS.items():
        d = summary["metrics"][key]
        cells.append(f'<tr><th scope="row">{label}</th><td>{d["known"]}/{len(rows)}</td>'
                     f'<td>{measurement(d["total"])}</td><td>{measurement(d["median"])}</td>'
                     f'<td>{measurement(d["min"])} to {measurement(d["max"])}</td></tr>')
    band = summary["success_interval_95"]
    interval = (f'<p>95% Wilson interval for eventual success: {band[0]:.1%} to {band[1]:.1%}. '
                'This describes sampling uncertainty, not task-selection bias or a causal benefit.</p>') if len(rows) >= 5 else '<p>Too few observations for a useful stability conclusion.</p>'
    return f'''<p>Submitted tasks: {summary['submitted_tasks']}. First-attempt checked fixes: {summary['first_attempt_successes']}/{len(rows)}.
Eventual checked fixes: {summary['eventual_successes']}/{len(rows)}. False completion claims found: {summary['false_completions']};
claim review unknown for {summary['claim_reviews_missing']} tasks. Interruptions: {summary['interruptions']}; recovery observations: {summary['recovery_observations']}.</p>
<p>Recovery outcomes: {escape(json.dumps(summary['recovery_outcomes'], sort_keys=True))}. Outcome categories: {escape(json.dumps(summary['outcomes'], sort_keys=True))}.</p>
{interval}<div class="table-scroll"><table><thead><tr><th>Measurement</th><th>Known / submitted</th><th>Total</th><th>Median of known values</th><th>Range of known values</th></tr></thead><tbody>{''.join(cells)}</tbody></table></div>
<p>Any missing input makes the total unknown. Medians and ranges use only known values with their sample counts shown.
Full-run totals include all attempts. An unconfirmed pass is an evidence problem, not a false model claim.</p>'''


def event_description(event):
    kind = event["kind"]
    detail = ""
    if kind.startswith("command_"):
        check = event.get("check")
        if not isinstance(check, dict):
            return "Unreadable command event", "Inspect the original journal."
        detail = f"{check.get('id', 'Unnamed check')}: {check.get('outcome', 'incomplete')}"
        if kind == "command_finished":
            detail += f"; exit {check.get('exit_code')}; {measurement(check.get('duration_seconds'), ' s')}"
    elif kind.startswith("native_tool_"):
        tool = event.get("request" if kind.endswith("started") else "result")
        if not isinstance(tool, dict):
            return "Unreadable tool event", "Inspect the original journal."
        detail = str(tool.get("name", "Unknown tool"))
        if kind.endswith("finished"):
            detail += ": failed" if tool.get("error") else ": returned"
    elif kind.startswith("verification_"):
        check = event.get("verification")
        if not isinstance(check, dict):
            return "Unreadable verification event", "Inspect the original journal."
        detail = f"{check.get('outcome', 'incomplete')}; run {check.get('run_id', 'unknown')}"
    elif event.get("outcome"):
        detail = str(event["outcome"])
    labels = {"native_tool_started": "Tool started", "native_tool_finished": "Tool returned",
              "verification_started": "Independent checks started", "verification_finished": "Independent checks finished",
              "workflow_finished": "Run stopped; human review remains separate",
              "workflow_candidate_checked": "Candidate checked", "model_request": "Model request",
              "model_usage": "Model usage recorded", "native_snapshot_captured": "Source snapshot saved"}
    return labels.get(kind, kind.replace("_", " ").capitalize()), detail


def timeline(row, reports, output):
    items = []
    starts = {}
    for event in row["events"]:
        label, detail = event_description(event)
        kind = event["kind"]
        data = event.get("check") or event.get("verification") or event.get("request") or event.get("result")
        data = data if isinstance(data, dict) else {}
        identity = data.get("id", data.get("run_id", data.get("name")))
        phase = kind.removesuffix("_started").removesuffix("_finished")
        if kind.endswith("_started"):
            starts[(phase, str(identity))] = event.get("at")
        elif kind.endswith("_finished") and (phase, str(identity)) in starts:
            try:
                elapsed = (datetime.fromisoformat(event["at"]) - datetime.fromisoformat(starts.pop((phase, str(identity))))).total_seconds()
                detail += f"; observed elapsed {measurement(elapsed, ' s')}"
            except (ValueError, TypeError, KeyError):
                detail += "; elapsed time unavailable"
        items.append(f'<li><time>{escape(event.get("at", "Time unknown"))}</time> '
                     f'<strong>{escape(label)}</strong> {escape(detail)}</li>')
    warnings = ''.join(f'<p class="warning">{escape(p)}</p>' for p in row["event_problems"])
    if row.get("problem"):
        warnings += f'<p class="warning">{escape(row["problem"])}</p>'
    receipt = row.get("receipt_snapshot", {})
    measured = row["measurement"]
    attempts = []
    for attempt in row.get("verification_attempts", []):
        if not isinstance(attempt, dict) or not isinstance(attempt.get("checks", []), list):
            attempts.append('<li>Unreadable attempt details; inspect the receipt.</li>')
            continue
        checks = ''.join(f'<li>{escape(c.get("id", "Unnamed check"))}: {escape(c.get("outcome", "incomplete"))}'
                         f'; exit {escape(c.get("exit_code"))}</li>' if isinstance(c, dict) else '<li>Unreadable check details.</li>'
                         for c in attempt.get("checks", []))
        attempts.append(f'<li>Attempt result: {escape(attempt.get("outcome", "incomplete"))}. '
                        f'Exact patch: <code>{escape(attempt.get("patch_sha256", "Unknown"))}</code><ul>{checks}</ul></li>')
    evidence = link(reports / row["receipt"], output, "Receipt") if row.get("receipt_sha256") else "Receipt unavailable"
    title = ("Proposed repair passed all independent checks" if row["verified_success"] else
             "Saved pass could not be confirmed" if row["unconfirmed_completion"] else
             "This run did not establish a checked repair")
    if row.get("demonstration"):
        title = "Supplied success scenario; no checks ran" if row["verified_success"] else "Supplied failure/stop scenario; no checks ran"
    duplicate = ('<p class="warning">Duplicate run ID. Conflicting evidence: all copies excluded from group totals; inspect both receipts.</p>'
                 if row["conflicting_duplicate"] else '<p>Identical run replay. Counted once through its canonical receipt; this copy is excluded.</p>') if row["duplicate"] else ''
    human = row.get("operator_evidence") or {}
    human_events = human.get("record", {}).get("events", [])
    handoff = ''.join(f'<li>{escape(e.get("at", "Unknown time"))}: {escape(e.get("action", "Unknown action"))}; '
                      f'{escape(e.get("phase", "unknown phase"))}; {escape(e.get("actor", "unknown actor"))}</li>'
                      for e in human_events if isinstance(e, dict))
    checks = ''.join(f'<li>{escape(c["id"])}: {escape(c["outcome"])}; exit {escape(c["exit_code"])}; '
                     f'{measurement(c["duration_seconds"], " s")}; {escape(c["started_at"] or "start unknown")} to '
                     f'{escape(c["ended_at"] or "end unknown")}. {link(reports / c["receipt"], output, "Check receipt")}. '
                     f'Candidate: <code>{escape(c["patch_sha256"] or "Not recorded")}</code>.</li>' for c in measured["checks"])
    run_key = digest(row["receipt"].encode())[:16]
    pins = row.get("pins", {})
    return f'''<details class="run" id="run-{run_key}" data-workflow="{escape(row.get('workflow', 'Unknown'))}"
data-model="{escape(pins.get('model') or 'Unknown')}" data-task="{escape(measured['identity']['task'] or 'Unknown')}"
data-suite="{escape(measured['identity']['suite_version'])}" data-mode="{escape(row.get('mode', 'unknown'))}"
data-environment="{escape(digest(json.dumps(pins.get('environment'), sort_keys=True).encode())[:12])}">
<summary>{escape(report_visuals.run_name(row))}: {title}</summary>
<p>Run ID: <code>{escape(row['run_id'])}</code>. Recorded outcome: <code>{escape(row['outcome'])}</code>. {escape(row.get('mode', 'unknown'))} run.
{'Separate child check receipt; excluded from task totals.' if row['is_child'] else 'Task run.'}</p>{duplicate}
<p>Elapsed: {measurement(row.get('duration_seconds'), ' s')}. Model cost: {measurement(row.get('model_cost_usd'), ' USD')}.
Execution cost: {measurement(row.get('execution_cost_usd'), ' USD')}.
Human review time: {measurement(row.get('review_seconds'), ' s')}. Human acceptance: {escape(row.get('human_acceptance', 'unknown'))}.</p>
<p>{evidence}. Receipt hash: <code>{escape(row.get('receipt_sha256', 'Unknown'))}</code>.
Patch hash: <code>{escape(receipt.get('patch_sha256', 'Unknown'))}</code>.</p>
<p>{escape(report.OUTCOMES.get(row['outcome'], 'The recorded outcome is shown above; it does not imply human approval.'))}
{metric_overview(measured)}</p>
<p>{candidate_link(row, reports, output)}</p>
<details><summary>Read the saved receipt in this page</summary><pre>{escape(json.dumps(receipt, indent=2))}</pre></details>
<details><summary>Independent attempt results, including deliberate failing checks</summary>
<p>The planted bug should fail. A failed planted-bug check followed by passing repair checks is expected.</p>
<ol>{''.join(attempts) or '<li>No independent attempt results recorded.</li>'}</ol></details>
<details><summary>Timed checks and source receipts ({len(measured['checks'])})</summary><ol>{checks or '<li>No timed checks recorded.</li>'}</ol></details>
<details><summary>Human handoff and recorded participation</summary>
<p>Human acceptance: {escape(measured['human_acceptance'])}. Only explicitly recorded operator events count as participation.</p>
<ul>{handoff or '<li>No operator events recorded. This does not mean zero human attention.</li>'}</ul></details>
{warnings}<details><summary>Recorded step timeline ({len(items)} events)</summary>
<p>Journal order is preserved. Timestamps are recorded observations, not inferred durations. Repeated event IDs count once.
Journal hash: <code>{escape(row['events_sha256'] or 'Unknown')}</code>.</p>
<ol>{''.join(items) or '<li>No readable events recorded.</li>'}</ol></details></details>'''


def groups(records):
    selected = defaultdict(list)
    for row in records:
        if not row["is_child"] and not row["duplicate"]:
            key = (row.get("workflow"), row.get("mode"), row.get("synthetic"), row.get("simulated_checks"), row.get("settings_sha256"))
            selected[key].append(row)
    return list(selected.values())


def render(value, reports, output):
    sections = []
    for index, rows in enumerate(groups(value["records"]), 1):
        first = rows[0]
        pins = first.get("pins", {})
        outcomes = ', '.join(f'{name}: {count}' for name, count in sorted(Counter(r['outcome'] for r in rows).items()))
        points = ''.join(f'<li><code>{escape(r["run_id"])}</code>: {measurement(r.get("duration_seconds"), " s")}; '
                         f'{escape(r["outcome"])}</li>' for r in rows)
        finite = [r for r in rows if evaluate.number(r.get("duration_seconds")) is not None]
        maximum = max((r["duration_seconds"] for r in finite), default=1) or 1
        bars = ''.join(f'<div class="point"><code>{escape(r["run_id"])}</code>'
                       f'<meter min="0" max="{maximum}" value="{r["duration_seconds"]}" '
                       f'aria-label="Elapsed seconds for {escape(r["run_id"])}">{r["duration_seconds"]}</meter>'
                       f'<span>{measurement(r["duration_seconds"], " s")}</span></div>' for r in finite)
        data = first['measurement']['identity']
        charts = ''.join(chart(rows, key, reports, output) for key in ("first_attempt_success", "eventual_success", *METRIC_LABELS))
        changes = ''.join(f'<li>{escape(r.get("started_at") or "Time unknown")}: {escape(r["run_id"])}; '
                          f'harness <code>{escape(r["measurement"]["identity"]["harness_version"] or "Unknown")}</code></li>'
                          for r in sorted(rows, key=lambda r: str(r.get("started_at") or "")))
        sections.append(f'''<details class="identity-group" data-workflow="{escape(first.get('workflow', 'Unknown'))}"
data-model="{escape(pins.get('model') or 'Unknown')}" data-task="{escape(data['task'] or 'Unknown')}"
data-suite="{escape(data['suite_version'])}" data-mode="{escape(first.get('mode', 'unknown'))}"
data-environment="{escape(digest(json.dumps(pins.get('environment'), sort_keys=True).encode())[:12])}"><summary>Group {index}: {escape(first.get('workflow', 'Unknown workflow'))},
{escape(pins.get('model') or 'model unknown')}, {escape(first.get('workflow_mode') or first.get('mode', 'unknown'))}, {len(rows)} submitted tasks</summary>
<p>First-repair checked fixes: {sum(r['first_attempt_success'] for r in rows)}/{len(rows)}.
Eventual checked fixes: {sum(r['verified_success'] for r in rows)}/{len(rows)}. Outcomes: {escape(outcomes)}.</p>
<p>{'Demonstration data; excluded from live AI results.' if first.get('mode') != 'live' else 'Live AI attempts; synthetic tasks remain distinct from real bugs.'}
{'Missing identity fields prevent a claim of comparability.' if first.get('missing_pins') else 'Exact recorded identity fields match within this group.'}
{'No repeated runs in this group.' if len(rows) < 2 else 'Historical observations only; no causal improvement claim.'}</p>
<details><summary>Task, suite, settings and environment identity</summary><pre>{escape(json.dumps(pins, indent=2))}</pre>
<p>Missing fields: {escape(', '.join(first.get('missing_pins', [])) or 'None')}.</p></details>
<h3>Elapsed time for each recorded run</h3><p>Includes failed runs. A short failure is not a faster repair. Missing durations have no bar.</p>
{bars}<details><summary>Raw durations and outcomes</summary><ul>{points}</ul></details>
<details><summary>All metrics, uncertainty and descriptive calendar charts</summary>{summary_section(rows)}{charts}</details>
<details><summary>Harness versions on the observation timeline</summary><ul>{changes}</ul>
<p>Groups split when recorded harness/settings identities change. Do not pool different models or easier tasks to claim a harness improvement.</p></details></details>''')
    grouped = (f'<details><summary>Browse {len(sections)} identity groups</summary>{"".join(sections)}</details>'
               if sections else '<p>No runs recorded. No trends or success rates can be established.</p>')
    runs = ''.join(timeline(row, reports, output) for row in sorted(value["records"], key=lambda r: str(r.get("started_at") or ""), reverse=True))
    paired = []
    for comparison in value.get("comparisons", []):
        if not comparison["complete"]:
            paired.append(f'<p class="warning">Comparison {escape(comparison["id"])} unavailable: {escape(comparison["problem"])}</p>')
            continue
        modes = []
        for mode in ("plain", "managed"):
            rows = [r for r in comparison["rows"] if r.get("workflow_mode") == mode]
            modes.append(f'<h4>{mode}: {len(rows)} submitted tasks</h4>{summary_section(rows)}' +
                         ''.join(chart(rows, key, reports, output) for key in ("eventual_success", "duration_seconds", "total_cost_usd", "review_seconds")))
        paired.append(f'<details><summary>Validated paired plan {escape(comparison["id"])}: {escape(comparison["task"])} '
                      f'({escape(comparison["partition"])})</summary><p>{link(reports / comparison["plan"], output, "Frozen plan")}. '
                      f'Plan hash: <code>{comparison["plan_sha256"]}</code>. '
                      'Only workflow mode differs within the validated plan. Development tasks cannot support a held-out claim. '
                      'A held-out plan supports this small within-sample comparison, not general reliability or human time savings.</p>'
                      f'{"".join(modes)}</details>')
    project = project_section(value, reports, output)
    exclusions = Counter("child checker receipt" if r["is_child"] else "duplicate run ID" if r["duplicate"] else
                         "live task" if r.get("mode") == "live" else "demonstration or unknown origin" for r in value["records"])
    projection = json.dumps({"schema_version": measurements.VERSION, "progress_sha256": value.get("progress_sha256"),
                             "records": [r["measurement"] for r in value["records"]]}, sort_keys=True).replace('<', '\\u003c')
    filters = ''.join(f'<label>{label} <select data-filter="{key}"><option value="">All</option></select></label>'
                      for key, label in (("workflow", "Workflow"), ("task", "Task set"), ("suite", "Evaluation suite"),
                                         ("model", "Model/settings groups"), ("environment", "Environment"), ("mode", "Live / demonstration")))
    changes = []
    previous = None
    for row in sorted((r for r in value["records"] if not r["is_child"] and not r["duplicate"] and r.get("mode") == "live"), key=lambda r: str(r.get("started_at") or "")):
        version = row["measurement"]["identity"]["harness_version"]
        if version != previous or not changes:
            changes.append(f'<li>{escape(row.get("started_at") or "Time unknown")}: harness <code>{escape(version or "Unknown")}</code>; '
                           f'{link(reports / row["receipt"], output, row["run_id"])}; model {escape(row.get("pins", {}).get("model") or "Unknown")}.</li>')
            previous = version
    return f'''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Harness run evidence</title><style>
:root{{color-scheme:light dark;background:Canvas;color:CanvasText;font:16px/1.6 system-ui,sans-serif}}body{{max-width:72rem;margin:auto;padding:1.5rem;overflow-wrap:anywhere}}
h1,h2,h3{{line-height:1.25}}details{{border:1px solid #888;border-radius:.4rem;padding:1rem;margin:1rem 0}}
summary{{cursor:pointer;padding:.3rem;font-weight:600}}summary:focus-visible,a:focus-visible{{outline:3px solid #5686dc;outline-offset:4px}}
code,pre,time{{overflow-wrap:anywhere}}pre{{white-space:pre-wrap}}li{{margin:.5rem 0}}time{{display:block;font-size:.9rem}}
.warning{{border-left:4px solid #b77a00;padding-left:1rem}}.point{{display:flex;flex-wrap:wrap;gap:1rem;align-items:center;margin:.5rem 0}}
.point code{{flex:1 1 22rem}}meter{{flex:1 1 12rem;max-width:100%}}a{{text-underline-offset:.2em}}
.table-scroll{{overflow-x:auto}}table{{border-collapse:collapse;width:100%;font-size:.9rem}}th,td{{text-align:left;padding:.5rem;border-bottom:1px solid #888;overflow-wrap:anywhere}}
svg{{width:100%;height:180px}}circle,svg text{{fill:currentColor}}figure{{margin:1rem 0}}form{{display:flex;flex-wrap:wrap;gap:1rem}}select{{max-width:100%;font:inherit;padding:.4rem}}label{{max-width:100%}}[hidden]{{display:none!important}}
{report_visuals.STYLES}
</style><body><a class="skip-link" href="#overview">Skip to results</a><main><header><h1>How did the runs turn out?</h1>
<p>Results from saved runs. Select an attempt to inspect its checks.</p>
<p class="report-meta">Snapshot saved {escape(report_visuals.snapshot_time(value['generated_at']))}. Not a live feed.</p></header>
{'<p class="warning"><strong>Supplied synthetic demonstration. No model or product checks ran.</strong> Success flags in this page are test expectations, not verified repairs. Excluded from real measurements.</p>' if value.get('demonstration') else ''}
<nav class="report-nav" aria-label="Report sections"><a href="#overview">Results</a><a href="#runs">Inspect attempts</a><a href="#project">Project progress</a><a href="#reference">Help and definitions</a></nav>
{report_visuals.overview(value)}
<details class="secondary-section"><summary>Project progress and questions for review</summary>{project}</details>
<details class="secondary-section"><summary>Detailed filters, measurements and comparisons</summary>
<h2>Choose which evidence to inspect</h2><form aria-label="Filter recorded evidence">{filters}<button type="reset">Reset filters</button></form>
<p>These filters narrow the detailed records below. The results at a glance show all saved attempts of the selected run type.</p>
<p id="filter-status" role="status">Filters select complete identity groups. Group denominators never change silently.</p>
<p>Inventory: {escape(json.dumps(dict(exclusions), sort_keys=True))}. All excluded records remain in the run list.
Demonstration and unknown-origin groups are excluded from live AI measurements.</p>
<h2 id="groups">Grouped observations</h2><p>Groups keep workflow, task, suite, model/settings and environment fingerprints separate.
Prepared examples are not measured AI repairs. Child check receipts and duplicate run IDs are excluded from task totals.
All submitted tasks within each group count in its denominator, including failures, cancellations and incomplete records.
These are historical groups, not a paired experiment. Unknown billing and human time stay unknown.</p>
{grouped}
<details><summary>Harness changes across live observations</summary><ul>{''.join(changes) or '<li>No live runs recorded.</li>'}</ul>
<p>Different task, model or environment choices can explain a change. This timeline does not attribute the results to the harness.</p></details>
<h2 id="paired">Controlled comparisons</h2>
{''.join(paired) or '<p>No paired comparison evidence available here. No controlled improvement graph is invented. The historical charts above remain descriptive.</p>'}
</details>
<h2 id="runs">Run timelines and evidence</h2><p>Open a run for its checks and journal. Deliberate failures test the checker;
they are not evidence of model failure. Raw source files remain local and must accompany this HTML for evidence links to work.</p>
{runs or '<p>No runs recorded.</p>'}
<p>Project milestones and learning decisions remain in the stage report, separate from these execution results.
See <a href="#reference">the shared glossary and file guide</a>.</p>
<details class="secondary-section" id="reference"><summary>Help, definitions and how this report was built</summary><h2>Reference and worked example</h2>
<p>Source: <code>{escape(reports)}</code>. This page reads existing receipts and journals. It makes no model calls and runs no product tests.
A receipt is a saved result; a journal records steps. Passing checks and human approval are separate.</p>
<p>Original code passes → the deliberately broken code fails → a working proposed repair passes, or an ineffective repair still fails.
The middle failure is expected: it proves the example exposes the planted bug. A passing repair still needs human review.
Pi chooses tools and proposes edits. Our runner owns limits, evidence and checks. This report only reads those saved records.</p>
<details><summary>Shared glossary</summary><dl>{report.definitions(report.GLOSSARY)}</dl></details>
<details><summary>Which files should I open?</summary><p>Start with telemetry.html for this walkthrough and index.html for saved stage reports.
Open a run's receipt and exact candidate when reviewing a result. Command logs and source archives are for investigations.</p>
<dl>{report.definitions(report.FILE_GUIDE)}</dl></details></details>
<script type="application/json" id="measurement-data">{projection}</script>
<script>
const filters=[...document.querySelectorAll('[data-filter]')];
const entries=[...document.querySelectorAll('.run,.identity-group')];
for(const field of filters){{
  for(const value of [...new Set(entries.map(e=>e.dataset[field.dataset.filter]))].sort()){{
    const option=document.createElement('option');option.value=value;option.textContent=value;field.append(option);
  }}
}}
function applyFilters(){{
  for(const entry of entries)entry.hidden=filters.some(f=>f.value&&entry.dataset[f.dataset.filter]!==f.value);
  const runs=entries.filter(e=>e.classList.contains('run')&&!e.hidden).length;
  const groups=entries.filter(e=>e.classList.contains('identity-group')&&!e.hidden).length;
  document.getElementById('filter-status').textContent=`Showing ${{runs}} receipts and ${{groups}} complete identity groups. Denominators include every submitted task in each shown group.`;
}}
document.querySelector('form').addEventListener('change',applyFilters);
document.querySelector('form').addEventListener('reset',()=>setTimeout(applyFilters,0));
applyFilters();
function chooseOverview(mode){{
  for(const control of document.querySelectorAll('[data-overview-switch]'))control.setAttribute('aria-pressed',String(control.dataset.overviewSwitch===mode));
  for(const panel of document.querySelectorAll('[data-overview-panel]'))panel.hidden=panel.dataset.overviewPanel!==mode;
  for(const field of filters)field.value=field.dataset.filter==='mode'?mode:'';
  applyFilters();
}}
for(const button of document.querySelectorAll('[data-overview-switch]'))button.addEventListener('click',()=>chooseOverview(button.dataset.overviewSwitch));
chooseOverview(document.querySelector('[data-overview-switch][aria-pressed=true]').dataset.overviewSwitch);
function revealSection(){{
  const target=document.getElementById(location.hash.slice(1));
  if(!target)return;
  for(let parent=target;parent;parent=parent.parentElement)if(parent.tagName==='DETAILS')parent.open=true;
  if(target.classList.contains('run')){{
    for(const field of filters)if(field.value&&target.dataset[field.dataset.filter]!==field.value)field.value='';
    applyFilters();
    target.querySelector('summary').focus();
  }}
  target.scrollIntoView();
}}
window.addEventListener('hashchange',revealSection);
for(const link of document.querySelectorAll('.runtime-label[href]'))link.addEventListener('click',()=>{{if(link.hash===location.hash)revealSection();}});
revealSection();
</script>
</main></body></html>'''


def main():
    parser = argparse.ArgumentParser(description="Build an offline timeline and grouped observations from existing local evidence; no model calls.")
    parser.add_argument("--reports", type=Path, default=REPORTS)
    parser.add_argument("--output", type=Path, help="HTML destination; defaults to REPORTS/telemetry.html")
    parser.add_argument("--progress", type=Path, default=HERE / "progress.json", help="Recorded project decisions; never inferred from tests")
    args = parser.parse_args()
    reports = args.reports.resolve()
    output = (args.output or reports / "telemetry.html").resolve()
    return build(reports, output, args.progress)


def build(reports, output, progress):
    value = load(reports, progress)
    output.parent.mkdir(parents=True, exist_ok=True)
    page = render(value, reports, output)
    with tempfile.TemporaryDirectory(prefix=".telemetry-", dir=output.parent) as temporary:
        staged = Path(temporary) / output.name
        staged.write_text(page)
        for row in value["records"]:
            directory = reports / Path(row["receipt"]).parent
            sources = [(reports / row["receipt"], row.get("receipt_sha256")),
                       (directory / "events.jsonl", row["events_sha256"])]
            operator = row.get("operator_evidence") or {}
            review = row["measurement"]["claim_review"] or {}
            sources.extend((reports / entry["path"], entry["sha256"]) for entry in (operator, review) if entry.get("path") and entry.get("sha256"))
            for path, expected in sources:
                if expected and digest(measurements.read_local(path, reports)) != expected:
                    raise ValueError("Evidence changed while rendering; previous report retained")
            if row["verified_success"] and not evaluate.verified(row["receipt_snapshot"], directory, reports):
                raise ValueError("Verification evidence changed while rendering; previous report retained")
        for comparison in value["comparisons"]:
            if comparison.get("plan") and digest(measurements.read_local(reports / comparison["plan"], reports)) != comparison["plan_sha256"]:
                raise ValueError("Comparison plan changed while rendering; previous report retained")
        if digest(progress.read_bytes()) != value["progress_sha256"]:
            raise ValueError("Project decisions changed while rendering; previous report retained")
        staged.replace(output)
    print(output)
    return int(any(r["conflicting_duplicate"] or r["event_problems"] or r["unconfirmed_completion"] or r.get("problem") for r in value["records"]) or
               any(not c["complete"] for c in value["comparisons"]))


if __name__ == "__main__":
    raise SystemExit(main())
