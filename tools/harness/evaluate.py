import argparse
from collections import Counter
import json
import math
from pathlib import Path
import re
import uuid
from types import SimpleNamespace

from repair import repair_receipt_matches
import operator_log
from run import REPORTS, Stop, digest, now, write_json
from verify import SCOPE, fingerprint, receipt_matches

FOLDERS = ("runs", "pi/runs", "verification/runs", "repair/runs", "recovery/runs",
           "native-baseline/runs", "native-tools/runs", "workflows/runs")
PINS = ("task_sha256", "base_commit", "evaluator_sha256", "verification_scope", "image",
        "provider", "model", "auth", "reasoning", "pi_version", "budgets", "environment",
        "runtime", "source_sha256", "runner_sha256", "container_runner_sha256", "seccomp_sha256", "mode", "config")


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
    if result.get("workflow") == "native-repair-workflow":
        from workflow import validated_result
        checked = validated_result(directory, reports)
        return checked.get("independent_checks_passed") is True and checked["outcome"] == "native_verification_passed"
    if result.get("workflow") in ("plain-pi-live-baseline", "pi-native-tools"):
        from native_check import verification_matches
        record = result.get("verification", {})
        identity = record.get("run_id")
        if not isinstance(identity, str) or not re.fullmatch(r"[0-9a-f]{32}", identity):
            return False
        child = reports / "verification/runs" / identity
        if not local_evidence(child, reports):
            return False
        raw = (child / "result.json").read_bytes()
        return (result.get("outcome") == "native_verification_passed" and result.get("independent_checks_passed") is True and
                digest(raw) == record["receipt_sha256"] and digest((directory / "candidate.patch").read_bytes()) == result["patch_sha256"] and
                digest((directory / "candidate-source.bin").read_bytes()) == result["snapshot_source_sha256"] and
                verification_matches(SimpleNamespace(path=directory, result=result), SimpleNamespace(path=child, result=json.loads(raw))))
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
    claims_success = result.get("outcome") in ("verified_repair_passed", "repository_verification_passed", "native_verification_passed")
    attempts = result.get("attempts", result.get("verification_attempts", []))
    if not attempts and result.get("verification"):
        attempts = [result["verification"]]
    if not isinstance(attempts, list):
        attempts = []
    pins = {key: result.get(key) for key in PINS}
    context = path.parent / "context.json"
    try:
        pins["context_sha256"] = fingerprint(json.loads(context.read_text())) if context.exists() else None
    except (OSError, ValueError, TypeError):
        pins["context_sha256"] = None
    mode = "live" if result.get("candidate_source") == "AI" else "prepared" if (
        result.get("candidate_source") in ("prepared", "supplied") or
        result.get("model") == "scripted (no AI)" or result.get("model") is None) else "unknown"
    operator = result.get("operator") or {}
    operator_evidence = None
    operator_path = path.parent / "operator.json"
    if operator_path.exists() or operator_path.is_symlink():
        operator_evidence = {"path": operator_path.relative_to(reports).as_posix(), "sha256": None}
        try:
            if operator_path.is_symlink():
                raise ValueError("Linked operator evidence needs inspection")
            operator_raw = operator_path.read_bytes()
            operator_evidence["sha256"] = digest(operator_raw)
            operator_record = json.loads(operator_raw)
            operator = operator_log.summary(path.parent, operator_record)
            operator_evidence["record"] = operator_record
        except (OSError, ValueError, KeyError, TypeError, Stop):
            operator = {}
            operator_evidence["problem"] = "Supplemental operator timing needs inspection; human measurements stay unknown"
    intervals = operator.get("recorded_intervals_seconds") or {}
    return {
        "receipt": path.relative_to(reports).as_posix(), "receipt_sha256": digest(raw),
        "receipt_snapshot": result,
        "operator_evidence": operator_evidence,
        "run_id": result.get("run_id", path.parent.name), "workflow": result.get("workflow", "host-calibration"),
        "parent_run_id": result.get("parent_run_id"), "started_at": result.get("started_at"),
        "outcome": result.get("outcome", "incomplete"), "synthetic": result.get("synthetic"), "mode": mode,
        "workflow_mode": result.get("mode"),
        "simulated_checks": result.get("evidence_origin") == "simulated-check-output",
        "pins": pins, "settings_sha256": fingerprint(pins),
        "missing_pins": [key for key, value in pins.items() if value is None],
        "verified_success": passed, "unconfirmed_completion": claims_success and not passed,
        "first_attempt_success": passed and (result.get("first_repair_passed") is True if result.get("workflow") == "native-repair-workflow"
                                             else not attempts or len(attempts) == 1),
        "verification_attempts": attempts,
        "duration_seconds": number(result.get("duration_seconds")),
        "token_summary": result.get("token_summary", result.get("usage_summary")),
        "model_cost_usd": number(result.get("model_cost_usd")),
        "execution_cost_usd": number(result.get("execution_cost_usd")),
        "user_corrections": sum(c.get("origin") == "operator" for c in result.get("continuations", [])) if operator.get("participation") == "recorded" else None,
        "review_seconds": number(intervals.get("reviewing")) if operator.get("timing_complete") and intervals.get("reviewing", 0) > 0 else None,
        "attention_seconds": number(operator.get("attention_seconds")),
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
    child_paths = {f"verification/runs/{item.get('verification_run_id', item.get('run_id'))}/result.json"
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
    lines = ["# What happened in the saved runs?", "",
             "I want to count the failed attempts too, so a good result doesn't hide the work it took to get there.",
             "This script reads saved results and checks their supporting files. It makes no model calls.",
             "These runs happened at different times. This is not a controlled comparison: I haven't held the tasks and settings fixed to test one change.", "",
             f"I've found {summary['runs']} task runs and {summary['child_receipts']} separate check results within those runs.",
             f"Passed every required check on the first repair: {summary['first_attempt_successes']}/{summary['runs']}.",
             f"Passed every required check by the end of the run: {summary['eventual_successes']}/{summary['runs']}.",
             "The runs include different kinds of examples, so these totals don't tell me how reliable the AI is.",
             f"Saved passes I need to inspect: {summary['unconfirmed_completions']}.",
             f"Missing or unreadable result files: {summary['unreadable_receipts']}.",
             "I won't use an unconfirmed pass to justify a change. Missing evidence doesn't tell me whether the repair was wrong.",
             "I still need to measure incorrect success claims, my corrections and my review time.", "",
             "## Every task outcome", ""]
    lines.extend(f"- {name}: {count}" for name, count in summary["outcomes"].items())
    if not summary["runs"]:
        lines.append("No local runs found. I need to run the examples before there's anything to compare.")
    lines.extend(["", "## Each saved run", ""])
    for row in value["records"]:
        if row["verified_success"]:
            label = "The repair passed every required check; my review is still pending"
        elif row["unconfirmed_completion"]:
            label = "I couldn't confirm this saved pass"
        else:
            label = "This run hasn't established a checked repair"
        lines.extend([f"- {row['receipt']}: {label}. Saved outcome: {row['outcome']}.",
                      f"  Task: {'a deliberately planted bug' if row.get('synthetic') is True else 'origin not recorded'}. Run type: {row.get('mode', 'unknown')}.",
                      f"  Details: {row.get('problem', 'inventory.json keeps the saved result, its file fingerprint and the path to the original evidence.')}"])
        if row.get("simulated_checks"):
            lines.append("  I supplied this example's test replies. No model or Go checks ran for this example.")
    lines.extend(["", "## What I can take from this", "",
                  "A passing retry doesn't erase its earlier failed repair. The JSON keeps every recorded check attempt.",
                  "One task can contain several checks. I count the task once, and its total time and cost once.",
                  "If settings, billing, tokens, corrections or review time weren't recorded, they're unknown.",
                  "Prepared examples check our code's behaviour. They don't establish how well an AI repairs real bugs.",
                  "My baseline is me supervising an AI. Controlled workflow comparisons and explicitly recorded human attention stay separate from these historical totals.",
                  "I still need to review the results. Passing tests and my approval are separate.",
                  "A receipt is a saved result file. A fingerprint identifies its exact contents; it doesn't prove the code is correct.",
                  "See the [shared glossary and file guide (#111)](https://github.com/adamcbrewer/mapbox-tui/issues/111).", ""])
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Read every saved harness attempt, including failures. This doesn't run an AI comparison.")
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
