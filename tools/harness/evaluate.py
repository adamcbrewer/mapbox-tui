import argparse
from collections import Counter
import json
import math
from pathlib import Path
import re
import uuid

from repair import repair_receipt_matches
from run import REPORTS, Stop, digest, now, write_json
from verify import SCOPE, fingerprint, receipt_matches

FOLDERS = ("runs", "pi/runs", "verification/runs", "repair/runs", "recovery/runs")
PINS = ("task_sha256", "base_commit", "evaluator_sha256", "verification_scope", "image",
        "provider", "model", "auth", "reasoning", "pi_version", "budgets", "environment",
        "runtime", "source_sha256", "runner_sha256", "container_runner_sha256", "seccomp_sha256")


def number(value):
    return value if type(value) in (int, float) and math.isfinite(value) and value >= 0 else None


def local_evidence(directory, reports):
    current = reports
    for part in directory.relative_to(reports).parts:
        current = current / part
        if current.is_symlink():
            return False
    return directory.is_dir() and not any(item.is_symlink() for item in directory.rglob("*"))


def verified(result, directory, reports):
    if result.get("verification_scope") != SCOPE:
        return False
    for attempt in result.get("attempts", []):
        identity = attempt.get("verification_run_id")
        if not isinstance(identity, str) or not re.fullmatch(r"[0-9a-f]{32}", identity):
            return False
        child = reports / "verification/runs" / identity
        if not local_evidence(child, reports):
            return False
    if result.get("recovery"):
        from recovery import validate_entries
        try:
            journal = json.loads((directory / "journal.json").read_text())
            task = json.loads((directory / "task.json").read_text())
            validate_entries(journal, task)
            if (journal["schema_version"] != 1 or journal["run_id"] != directory.name or
                    journal["result"] != result or journal["status"] != "complete" or
                    (directory / "recovery-stop.json").exists()):
                return False
        except (OSError, ValueError, KeyError, TypeError, AttributeError, Stop):
            return False
    if result.get("workflow") == "pi-verified-repair":
        return (result.get("outcome") == "verified_repair_passed" and
                repair_receipt_matches(result, directory, reports))
    return (result.get("workflow") == "frozen-patch-verification" and
            result.get("outcome") == "repository_verification_passed" and receipt_matches(result, directory))


def record(path, reports):
    raw = path.read_bytes()
    result = json.loads(raw)
    if not isinstance(result, dict):
        raise ValueError("Receipt must be an object")
    if not isinstance(result.get("outcome", "incomplete"), str):
        raise ValueError("Receipt outcome must be text")
    try:
        passed = verified(result, path.parent, reports)
    except (OSError, ValueError, KeyError, TypeError, AttributeError, Stop):
        passed = False
    claims_success = result.get("outcome") in ("verified_repair_passed", "repository_verification_passed")
    attempts = result.get("attempts", [])
    if not isinstance(attempts, list):
        attempts = []
    pins = {key: result.get(key) for key in PINS}
    mode = "live" if result.get("candidate_source") == "AI" else "prepared" if (
        result.get("candidate_source") == "prepared" or
        result.get("model") == "scripted (no AI)" or result.get("model") is None) else "unknown"
    return {
        "receipt": path.relative_to(reports).as_posix(), "receipt_sha256": digest(raw),
        "receipt_snapshot": result,
        "run_id": result.get("run_id", path.parent.name), "workflow": result.get("workflow", "host-calibration"),
        "parent_run_id": result.get("parent_run_id"), "started_at": result.get("started_at"),
        "outcome": result.get("outcome", "incomplete"), "synthetic": result.get("synthetic"), "mode": mode,
        "simulated_checks": result.get("evidence_origin") == "simulated-check-output",
        "pins": pins, "settings_sha256": fingerprint(pins),
        "missing_pins": [key for key, value in pins.items() if value is None],
        "verified_success": passed, "unconfirmed_completion": claims_success and not passed,
        "first_attempt_success": passed and (not attempts or len(attempts) == 1),
        "verification_attempts": attempts,
        "duration_seconds": number(result.get("duration_seconds")),
        "token_summary": result.get("token_summary"),
        "model_cost_usd": number(result.get("model_cost_usd")),
        "execution_cost_usd": number(result.get("execution_cost_usd")),
        "user_corrections": None, "review_seconds": None,
        "human_acceptance": result.get("human_acceptance", "pending"),
    }


def inventory(reports):
    records = []
    for folder in FOLDERS:
        for directory in sorted((reports / folder).glob("*")):
            if not directory.is_dir():
                continue
            path = directory / "result.json"
            try:
                if not local_evidence(directory, reports):
                    raise ValueError("Linked evidence needs inspection")
                row = record(path, reports)
            except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
                row = {"receipt": path.relative_to(reports).as_posix(), "run_id": directory.name,
                       "outcome": "evidence_unreadable", "problem": str(error),
                       "verified_success": False, "first_attempt_success": False,
                       "unconfirmed_completion": False, "parent_run_id": None}
            records.append(row)
    child_paths = {f"verification/runs/{item.get('verification_run_id')}/result.json"
                   for row in records for item in row.get("verification_attempts", []) if isinstance(item, dict)}
    for row in records:
        row["is_child"] = row.get("parent_run_id") is not None or row["receipt"] in child_paths
    roots = [row for row in records if not row["is_child"]]
    return {
        "schema_version": 1, "generated_at": now(), "kind": "historical-inventory",
        "source": str(reports.resolve()), "controlled_comparison": False,
        "records": records, "summary": {
            "runs": len(roots), "child_receipts": len(records) - len(roots),
            "outcomes": dict(sorted(Counter(row["outcome"] for row in roots).items())),
            "first_attempt_successes": sum(row["first_attempt_success"] for row in roots),
            "eventual_successes": sum(row["verified_success"] for row in roots),
            "unconfirmed_completions": sum(row["unconfirmed_completion"] for row in records),
            "unreadable_receipts": sum(row["outcome"] == "evidence_unreadable" for row in records),
        },
    }


def render(value):
    summary = value["summary"]
    lines = ["# Compare: historical run inventory", "",
             "Stage 5 of the harness roadmap collects comparable evidence before testing one change.",
             "This script reads saved receipts and rechecks their evidence. It makes no model calls.",
             "These are historical records, not a controlled comparison or proof that the harness improved.",
             "No model/settings were selected in advance and no baseline comparison has been run here.", "",
             f"Sample: {summary['runs']} root runs; {summary['child_receipts']} linked check receipts kept separately.",
             f"First repair passed every required check: {summary['first_attempt_successes']}/{summary['runs']}.",
             f"Final repair passed every required check: {summary['eventual_successes']}/{summary['runs']}.",
             "These mixed historical counts are an inventory, not a model success-rate estimate.",
             f"Saved completion claims needing inspection: {summary['unconfirmed_completions']}.",
             f"Unreadable or missing receipts needing inspection: {summary['unreadable_receipts']}.",
             "An unconfirmed completion blocks promotion; it can mean missing evidence, not necessarily a bad repair.",
             "False completion based on task outcomes, corrections and human review time remain unmeasured.", "",
             "## All root outcomes", ""]
    lines.extend(f"- {name}: {count}" for name, count in summary["outcomes"].items())
    if not summary["runs"]:
        lines.append("No local runs found. Generate run evidence first; missing data is not a success.")
    lines.extend(["", "## Every saved run", ""])
    for row in value["records"]:
        if row["verified_success"]:
            label = "Proposed repair passed every required check"
        elif row["unconfirmed_completion"]:
            label = "Saved pass could not be confirmed"
        else:
            label = "No independently verified success"
        lines.extend([f"- {row['receipt']}: {label}. Saved outcome: {row['outcome']}.",
                      f"  Case: {'synthetic' if row.get('synthetic') is True else 'provenance unknown'}. Mode: {row.get('mode', 'unknown')}.",
                      f"  Evidence: {row.get('problem', 'Receipt fingerprint and original evidence path retained in inventory.json.')}"])
        if row.get("simulated_checks"):
            lines.append("  Prepared demonstration with simulated check output. No model or Go checks ran for this example.")
    lines.extend(["", "## What this proves and what is next", "",
                  "Failures, cancellations, unfinished runs and unreadable evidence stay visible. Linked checks are not extra tasks.",
                  "First-attempt success requires a successful root run with one checked repair; eventual success allows repairs within that run.",
                  "Every recorded verification attempt remains in the JSON, including failed attempts before a successful repair.",
                  "Receipt settings and fingerprints identify historical inputs; matching fingerprints alone do not establish a controlled experiment.",
                  "Missing settings, billing, tokens, corrections or review time stay unknown. Root duration/cost already includes child work; do not add it twice.",
                  "Prepared and synthetic examples test harness behaviour, not real-world AI repair ability. Human acceptance remains separate.",
                  "Next: predeclare paired cases, one harness change and a primary metric, then establish human-plus-script and plain-Pi baselines.",
                  "Human review must still confirm the evidence and experiment design before drawing conclusions.",
                  "A receipt is the saved run result. A fingerprint identifies its exact bytes; it is not a correctness proof.",
                  "See the [shared glossary and file guide (#111)](https://github.com/adamcbrewer/mapbox-tui/issues/111).", ""])
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Inventory all local harness attempts without claiming a controlled comparison.")
    parser.add_argument("--reports", type=Path, default=REPORTS)
    parser.add_argument("--output", type=Path, help="New output directory; existing inventories are never overwritten")
    args = parser.parse_args()
    value = inventory(args.reports)
    output = args.output or args.reports / "compare/inventories" / uuid.uuid4().hex
    try:
        output.mkdir(parents=True, exist_ok=False)
    except FileExistsError:
        parser.error("Choose a new output directory; the previous inventory is retained")
    write_json(output / "inventory.json", value)
    (output / "inventory.md").write_text(render(value))
    print(output / "inventory.md")
    return 1 if value["summary"]["unconfirmed_completions"] or value["summary"]["unreadable_receipts"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
