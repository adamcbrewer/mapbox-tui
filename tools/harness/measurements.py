from collections import Counter
import json
import math
from pathlib import Path
import statistics

import evaluate
from run import Stop, digest


VERSION = 1
METRICS = ("duration_seconds", "repair_attempts", "retries", "tokens", "model_cost_usd", "execution_cost_usd",
           "total_cost_usd", "user_corrections", "attention_seconds", "review_seconds")


def read_local(path, reports):
    current = reports
    for part in path.relative_to(reports).parts:
        if part == "..":
            raise ValueError("Evidence path leaves its report directory")
        current /= part
        if current.is_symlink():
            raise ValueError("Linked measurement evidence needs inspection")
    return path.read_bytes()


def claim_review(row, reports):
    result = row.get("receipt_snapshot", {})
    comparison = result.get("comparison") or {}
    identity = comparison.get("experiment_id") if isinstance(comparison, dict) else None
    candidates = [reports / Path(row["receipt"]).parent / "claim-review.json"]
    if isinstance(identity, str) and len(identity) == 32 and all(c in "0123456789abcdef" for c in identity):
        candidates.append(reports / "workflow-pairs/experiments" / identity / "claim-review.json")
    for path in candidates:
        try:
            raw = read_local(path, reports)
            value = json.loads(raw)
            matches = [item for item in value["runs"] if item.get("run_id") == row["run_id"] and
                       item.get("receipt_sha256") == row.get("receipt_sha256")]
            if len(matches) == 1 and type(matches[0].get("incorrect_success_claim")) is bool:
                return {"incorrect_success_claim": matches[0]["incorrect_success_claim"],
                        "path": path.relative_to(reports).as_posix(), "sha256": digest(raw)}
        except (OSError, ValueError, KeyError, TypeError, AttributeError):
            continue
    return None


def category(row):
    if row["verified_success"]:
        return "verified_fix"
    outcome = row["outcome"]
    if row["unconfirmed_completion"]:
        return "unconfirmed_pass"
    if outcome in ("blocked", "calibration_failed", "evidence_unreadable"):
        return "environment_or_evidence_failure"
    if outcome in ("cancelled", "timeout", "incomplete", "budget_exhausted"):
        return outcome
    if outcome in ("repair_failed", "assertion_failed"):
        return "failed_repair"
    return "other_or_focused_result"


def normalize(row, by_path, reports):
    result = row.get("receipt_snapshot", {})
    attempts = row.get("verification_attempts", [])
    usable = [a for a in attempts if isinstance(a, dict)]
    raw_history = result.get("attempts", result.get("verification_attempts"))
    recorded_history = "attempts" in result or "verification_attempts" in result
    count = len(usable) if isinstance(raw_history, list) or not recorded_history and usable else None
    if len(usable) != len(attempts):
        count = None
    usage = row.get("token_summary")
    totals = usage.get("totals") if isinstance(usage, dict) else None
    tokens = evaluate.number(totals.get("totalTokens")) if isinstance(totals, dict) and usage.get("complete") is True else None
    model_cost = row.get("model_cost_usd")
    execution_cost = row.get("execution_cost_usd")
    recovery = result.get("recovery_outcome")
    recovered = bool(result.get("recovery")) or any("recover" in e["kind"] or "reconcil" in e["kind"] for e in row["events"])
    checks = []
    sources = [(row, None)]
    for number, attempt in enumerate(usable, 1):
        identity = attempt.get("verification_run_id", attempt.get("run_id"))
        child = by_path.get(f"verification/runs/{identity}/result.json")
        if child and attempt.get("receipt_sha256") == child.get("receipt_sha256"):
            sources.append((child, number))
    for source, attempt_id in sources:
        saved_checks = source.get("receipt_snapshot", {}).get("checks", [])
        for check in saved_checks if isinstance(saved_checks, list) else []:
            if not isinstance(check, dict):
                continue
            checks.append({"id": check.get("id"), "attempt": attempt_id, "started_at": check.get("started_at"),
                           "ended_at": check.get("ended_at"), "duration_seconds": evaluate.number(check.get("duration_seconds")),
                           "exit_code": check.get("exit_code"), "outcome": check.get("outcome"),
                           "stdout": check.get("stdout"), "stderr": check.get("stderr"),
                           "receipt": source["receipt"], "receipt_sha256": source.get("receipt_sha256"),
                           "patch_sha256": source.get("receipt_snapshot", {}).get("patch_sha256")})
    evaluator = result.get("evaluator") or {}
    review = claim_review(row, reports)
    task_name = result.get("task")
    if not isinstance(task_name, str):
        task_name = None
    if task_name is None:
        for name in ("verification-task.json", "task.json"):
            try:
                raw = read_local(reports / Path(row["receipt"]).parent / name, reports)
                if digest(raw) == result.get("task_sha256"):
                    saved_name = json.loads(raw).get("id")
                    task_name = saved_name if isinstance(saved_name, str) else None
                    break
            except (OSError, ValueError, AttributeError):
                continue
    return {"schema_version": VERSION, "run_id": row["run_id"], "receipt": row["receipt"],
            "receipt_sha256": row.get("receipt_sha256"), "events_sha256": row["events_sha256"],
            "role": {"is_child": row["is_child"], "duplicate": row["duplicate"], "conflicting_duplicate": row["conflicting_duplicate"]},
            "origin": {"mode": row.get("mode", "unknown"), "synthetic": row.get("synthetic"), "simulated_checks": row.get("simulated_checks", False)},
            "settings_sha256": row.get("settings_sha256"), "missing_pins": row.get("missing_pins", []),
            "identity": {"task": task_name, "fixture_sha256": result.get("task_sha256"),
                         "suite_version": evaluator.get("suite_version") if isinstance(evaluator, dict) else None,
                         "workflow": row.get("workflow"), "pins": row.get("pins", {}),
                         "base_sha256_or_commit": result.get("base_commit"), "patch_sha256": result.get("patch_sha256"),
                         "harness_version": result.get("runner_sha256"), "pi_version": result.get("pi_version")},
            "started_at": row.get("started_at"), "ended_at": result.get("ended_at"), "outcome": category(row),
            "recorded_outcome": row["outcome"], "first_attempt_success": row["first_attempt_success"],
            "eventual_success": row["verified_success"], "false_completion": review["incorrect_success_claim"] if review else None,
            "claim_review": review,
            "unconfirmed_completion": row["unconfirmed_completion"],
            "interrupted": row["outcome"] in ("cancelled", "timeout", "incomplete") or recovered,
            "recovery": {"observed": recovered, "outcome": recovery},
            "human_acceptance": row.get("human_acceptance", "pending"),
            "metrics": {"duration_seconds": row.get("duration_seconds"), "repair_attempts": count,
                        "retries": max(count - 1, 0) if count is not None else None, "tokens": tokens,
                        "model_cost_usd": model_cost, "execution_cost_usd": execution_cost,
                        "total_cost_usd": model_cost + execution_cost if model_cost is not None and execution_cost is not None else None,
                        "user_corrections": row.get("user_corrections"), "attention_seconds": row.get("attention_seconds"),
                        "review_seconds": row.get("review_seconds")},
            "cost_basis": result.get("cost_basis") if isinstance(result.get("cost_basis"), dict) else None, "checks": checks,
            "attempts": [{"id": a.get("verification_run_id", a.get("run_id")), "outcome": a.get("outcome"),
                          "patch_sha256": a.get("patch_sha256"), "receipt_sha256": a.get("receipt_sha256")} for a in usable]}


def distribution(values):
    known = [v for v in values if evaluate.number(v) is not None]
    return {"known": len(known), "missing": len(values) - len(known), "points": known,
            "total": sum(known) if len(known) == len(values) and known else None,
            "median": statistics.median(known) if known else None,
            "min": min(known) if known else None, "max": max(known) if known else None}


def interval(successes, count):
    if not count:
        return None
    z = 1.96
    proportion = successes / count
    centre = (proportion + z * z / (2 * count)) / (1 + z * z / count)
    radius = z * math.sqrt(proportion * (1 - proportion) / count + z * z / (4 * count * count)) / (1 + z * z / count)
    return [max(0, centre - radius), min(1, centre + radius)]


def summarize(rows):
    values = [row["measurement"] for row in rows]
    successes = sum(v["eventual_success"] for v in values)
    return {"submitted_tasks": len(values), "first_attempt_successes": sum(v["first_attempt_success"] for v in values),
            "eventual_successes": successes, "success_interval_95": interval(successes, len(values)),
            "outcomes": dict(sorted(Counter(v["outcome"] for v in values).items())),
            "false_completions": sum(v["false_completion"] is True for v in values),
            "claim_reviews_missing": sum(v["false_completion"] is None for v in values),
            "interruptions": sum(v["interrupted"] for v in values),
            "recovery_observations": sum(v["recovery"]["observed"] for v in values),
            "recovery_outcomes": dict(Counter(v["recovery"]["outcome"] or "unknown" for v in values if v["recovery"]["observed"])),
            "metrics": {key: distribution([v["metrics"][key] for v in values]) for key in METRICS}}


def comparisons(reports, records):
    import workflow_compare
    by_id = {row["run_id"]: row for row in records if not row["duplicate"]}
    values = []
    for directory in sorted((reports / "workflow-pairs/experiments").glob("*")):
        if not directory.is_dir():
            continue
        entry = {"id": directory.name, "rows": [], "problem": None}
        try:
            if not evaluate.local_evidence(directory, reports):
                raise ValueError("Linked comparison evidence")
            plan = workflow_compare.load_plan(directory, execution=False)
            entry.update(task=plan["common"]["case"]["id"], partition=plan["common"]["case"]["partition"],
                         plan_sha256=digest((directory / "plan.json").read_bytes()),
                         plan=(directory / "plan.json").relative_to(reports).as_posix())
            for cell in plan["cells"]:
                observed = workflow_compare.collect(directory, cell, reports=reports)
                row = by_id.get(observed.get("run_id"))
                if observed["status"] != "finished" or row is None or observed["receipt_sha256"] != row.get("receipt_sha256"):
                    raise ValueError("Comparison has an unfinished, missing or duplicate run; no success comparison is certified")
                entry["rows"].append(row)
            entry["complete"] = True
        except (Stop, OSError, ValueError, KeyError, TypeError, AttributeError) as error:
            entry.update(problem=error.reason if isinstance(error, Stop) else str(error), complete=False)
        values.append(entry)
    return values
