from collections import Counter
from datetime import datetime
import html
import statistics

from run import digest


def escape(value):
    return html.escape(str(value), quote=True)


def runtime(seconds):
    if seconds is None:
        return "Not recorded"
    seconds = round(seconds)
    return f"{seconds // 60}m {seconds % 60:02d}s" if seconds >= 60 else f"{seconds}s"


def snapshot_time(value):
    try:
        return datetime.fromisoformat(value).strftime("%d %b %Y at %H:%M UTC")
    except (ValueError, TypeError):
        return str(value)


def model_name(row):
    model = row.get("pins", {}).get("model")
    if not isinstance(model, str):
        return "Prepared example" if model is None and row.get("mode") == "prepared" else "Unknown model"
    return {"gpt-6.1-sol": "Sol", "gpt-6-luna": "Luna", "gpt-6.1-terra": "Terra",
            "scripted (no AI)": "Prepared example"}.get(model) or model or ("Prepared example" if row.get("mode") == "prepared" else "Unknown model")


def run_name(row):
    task = row["measurement"]["identity"]["task"]
    if not isinstance(task, str):
        task = None
    task = {"xml-leading-bom-v1": "XML import", "segment-distance-v1": "Journey distance"}.get(task, task or "Task not recorded")
    return f"{model_name(row)}: {task}"


OUTCOMES = {
    "verified": ("Fully checked fix", "success"),
    "focused": ("Passed a limited check", "limited"),
    "failed": ("Repair failed its check", "failure"),
    "blocked": ("Could not run the checks", "stopped"),
    "cancelled": ("Cancelled", "stopped"),
    "unfinished": ("Stopped or unfinished", "stopped"),
    "unconfirmed": ("Saved pass needs rechecking", "neutral"),
    "other": ("Needs inspection", "neutral"),
}


def outcome(row):
    if row["verified_success"]:
        return "verified"
    if row["unconfirmed_completion"]:
        return "unconfirmed"
    recorded = row["outcome"]
    if recorded in ("live_repair_passed", "calibration_passed", "scripted_loop_passed", "focused_verification_passed"):
        return "focused"
    if recorded in ("repair_failed", "assertion_failed"):
        return "failed"
    if recorded in ("blocked", "calibration_failed", "invalid_input", "evidence_unreadable"):
        return "blocked"
    if recorded == "cancelled":
        return "cancelled"
    if recorded in ("timeout", "incomplete", "budget_exhausted"):
        return "unfinished"
    return "other"


def bar(value, maximum, tone, label):
    width = 100 * value / maximum if maximum else 0
    return (f'<svg class="overview-bar {tone}" viewBox="0 0 100 10" preserveAspectRatio="none" role="img" aria-label="{escape(label)}">'
            f'<rect class="track" width="100" height="10" rx="2"/><rect width="{width:g}" height="10" rx="2"/></svg>')


def overview(value):
    roots = [r for r in value["records"] if not r["is_child"] and not r["duplicate"]]
    default = "prepared" if value.get("demonstration") else "live"
    buttons, panels = [], []
    for mode, label in (("live", "AI task attempts"), ("prepared", "Prepared test examples")):
        rows = sorted((r for r in roots if r.get("mode") == mode), key=lambda r: str(r.get("started_at") or ""))
        buttons.append(f'<button type="button" data-overview-switch="{mode}" aria-controls="overview-{mode}" '
                       f'aria-pressed="{str(mode == default).lower()}">{label} <span>{len(rows)}</span></button>')
        counts = Counter(outcome(r) for r in rows)
        durations = [r["measurement"]["metrics"]["duration_seconds"] for r in rows if r["measurement"]["metrics"]["duration_seconds"] is not None]
        costs = [r["measurement"]["metrics"]["total_cost_usd"] for r in rows]
        known_costs = [c for c in costs if c is not None]
        cost = f"${sum(known_costs):,.2f}" if costs and len(known_costs) == len(costs) else "Not recorded" if not known_costs else "Incomplete"
        cards = (("Saved attempts", str(len(rows)), "Includes setup failures and cancellations."),
                 ("Expected checked fixes" if value.get("demonstration") else "Fully checked fixes", str(counts["verified"]), f"Out of {len(rows)} saved attempts. Human review is separate."),
                 ("Typical run", runtime(statistics.median(durations)) if durations else "Not recorded", f"Median of {len(durations)} timed attempts, including stops."),
                 ("Total cost", cost, f"Complete billing recorded for {len(known_costs)} of {len(rows)} attempts."))
        stats = ''.join(f'<div class="stat"><dt>{name}</dt><dd><span data-stat="{index}">{escape(number)}</span><small>{escape(note)}</small></dd></div>'
                        for index, (name, number, note) in enumerate(cards))
        breakdown = ''.join(f'<div class="outcome-line"><span>{text}</span>{bar(counts[key], len(rows), tone, f"{text}: {counts[key]} of {len(rows)} attempts")}'
                            f'<strong>{counts[key]}</strong></div>' for key, (text, tone) in OUTCOMES.items() if counts[key])
        run_rows = []
        maximum = max(durations, default=1) or 1
        for index, row in enumerate(rows, 1):
            key = outcome(row)
            status, tone = OUTCOMES[key]
            seconds = row["measurement"]["metrics"]["duration_seconds"]
            try:
                date = datetime.fromisoformat(row["started_at"]).strftime("%d %b")
            except (ValueError, TypeError, KeyError):
                date = "Date not recorded"
            target = "run-" + digest(row["receipt"].encode())[:16]
            run_rows.append(f'<li><a class="runtime-label" href="#{target}"><strong>Attempt {index}: {escape(model_name(row))}</strong>'
                            f'<span>{date}. {escape(status)}.</span></a>'
                            f'{bar(seconds, maximum, tone, f"{runtime(seconds)}; {status}") if seconds is not None else "<span>Time not recorded</span>"}'
                            f'<strong class="runtime-number">{runtime(seconds) if seconds is not None else ""}</strong></li>')
        description = ("All saved AI task submissions, including attempts stopped before the model could repair anything."
                       if mode == "live" else "Prepared examples include deliberate failures. They do not measure AI repair ability.")
        panels.append(f'''<section id="overview-{mode}" data-overview-panel="{mode}" {'hidden' if mode != default else ''} aria-label="{label}">
<p class="overview-context">{description}</p><dl class="stats">{stats}</dl>
<div class="visual-grid"><section class="visual-panel"><h3>How did attempts end?</h3>
<p>A fully checked fix passed every required independent check. A limited pass is shown separately.</p>
{breakdown or '<p class="empty">No runs recorded for this view.</p>'}</section>
<section class="visual-panel"><h3>How long did each attempt take?</h3><p>Earlier attempts first. Select a row to inspect its evidence.</p>
<ol class="runtime-chart">{''.join(run_rows) or '<li>No timed attempts recorded.</li>'}</ol></section></div>
<p class="chart-note">Different tasks and models are shown here. This is a descriptive history, not a comparison proving that the harness improved.</p></section>''')
    unknown = sum(r.get("mode") not in ("live", "prepared") for r in roots)
    return f'''<section class="visual-overview" id="overview" aria-labelledby="overview-title"><h2 id="overview-title">The results at a glance</h2>
<div class="scope-controls" role="group" aria-label="Choose run type">{''.join(buttons)}</div>{''.join(panels)}
<p class="overview-context">{unknown} attempts have an unknown origin and are excluded from both views. Child check receipts and duplicate copies are excluded from these totals.</p></section>'''


STYLES = '''
:root{--paper:#f7f9fc;--panel:#fff;--ink:#172334;--muted:#526279;--rule:#cbd5e1;--success:#087f77;--limited:#6354a5;--failure:#bd3d50;--stopped:#9b5c06;--neutral:#607086;--track:#e6ebf2;background:var(--paper);color:var(--ink)}
@media(prefers-color-scheme:dark){:root{--paper:#111821;--panel:#182230;--ink:#e7edf5;--muted:#b4c0d0;--rule:#445268;--success:#60d7bd;--limited:#b3a4ff;--failure:#ff92a3;--stopped:#f1bd64;--neutral:#bcc8d9;--track:#344153}}
body{font-variant-numeric:tabular-nums}h1{font-size:clamp(1.8rem,4vw,2.7rem);letter-spacing:-.025em;margin:.2rem 0 .75rem}h2{font-size:1.4rem}h3{font-size:1.1rem}p{max-width:78ch}a{color:inherit}a:hover{text-decoration-thickness:2px}a:focus-visible,button:focus-visible,summary:focus-visible,select:focus-visible{outline:3px solid var(--limited);outline-offset:4px}button,select{font:inherit;color:var(--ink);background:var(--panel);border:1px solid var(--rule);border-radius:6px;padding:.6rem .9rem;cursor:pointer;min-height:44px}
.report-meta,.overview-context,.chart-note{color:var(--muted);font-size:.92rem}.report-nav{display:flex;gap:1.2rem;flex-wrap:wrap;margin:1.5rem 0 2rem}.scope-controls{display:flex;gap:.5rem;flex-wrap:wrap}.scope-controls button[aria-pressed=true]{background:var(--ink);color:var(--paper);border-color:var(--ink)}.scope-controls span{margin-left:.5rem;font-weight:700}.stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:1.5rem;margin:1.5rem 0 2rem;padding:1.5rem 0;border-block:1px solid var(--rule)}.stat{min-width:0}.stat dt{color:var(--muted);font-size:.95rem}.stat dd{margin:.3rem 0;font-size:clamp(1.5rem,3vw,2.4rem);font-weight:700;line-height:1.2;letter-spacing:-.025em}.stat small{display:block;margin:.5rem 0 0;color:var(--muted);font-size:.85rem;font-weight:400;line-height:1.5}
.visual-grid{display:grid;grid-template-columns:1fr 1.3fr;gap:1.5rem}.visual-panel{background:var(--panel);border:1px solid var(--rule);border-radius:10px;padding:1.25rem;min-width:0}.visual-panel h3{margin:0}.visual-panel>p{color:var(--muted);font-size:.9rem;min-height:3em}.outcome-line{display:grid;grid-template-columns:1.6fr 1fr 2ch;gap:.75rem;align-items:center;margin:1.1rem 0;font-size:.9rem}.overview-bar{display:block;width:100%;height:14px;min-width:0;fill:currentColor}.overview-bar .track{fill:var(--track)}.success{color:var(--success)}.limited{color:var(--limited)}.failure{color:var(--failure)}.stopped{color:var(--stopped)}.neutral{color:var(--neutral)}.runtime-chart{list-style:none;padding:0;margin:1rem 0}.runtime-chart li{display:grid;grid-template-columns:minmax(0,1.5fr) minmax(0,1fr) 5.5ch;gap:.75rem;align-items:center;margin:1rem 0;font-size:.9rem}.runtime-label{text-decoration:none;min-width:0}.runtime-label strong{display:block;font-weight:600}.runtime-label span{display:block;color:var(--muted);font-size:.8rem;margin-top:.2rem}.runtime-label:hover strong{text-decoration:underline}.runtime-number{text-align:right;white-space:nowrap;font-size:.85rem}.chart-note{border-left:3px solid var(--rule);padding-left:1rem;margin:1.25rem 0 2rem}details{background:var(--panel);border-color:var(--rule)}details>summary{min-height:2rem;align-content:center}.secondary-section{margin-top:1.75rem}section[id],details[id],h2[id]{scroll-margin-top:1rem}.skip-link{position:absolute;left:-10000px}.skip-link:focus{position:static;display:inline-block;padding:.5rem}#overview{margin-bottom:2rem}
@media(max-width:820px){.visual-grid{grid-template-columns:1fr}.stats{grid-template-columns:repeat(2,minmax(0,1fr))}.visual-panel>p{min-height:0}}
@media(max-width:440px){body{padding:1rem}.visual-panel{padding:1rem}.outcome-line{grid-template-columns:1.6fr 1fr 2ch;gap:.5rem}.runtime-chart li{grid-template-columns:minmax(0,1.4fr) minmax(0,.8fr) 5.5ch;gap:.5rem}.stats{gap:1rem}.stat dd{font-size:1.7rem}}
'''
