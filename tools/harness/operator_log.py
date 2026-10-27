import argparse
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import signal
import time

from recovery import durable_json
from run import Stop, now

STATES = {"monitoring", "away", "paused", "reviewing", "finished"}


def process_identity(pid):
    return {"pid": pid, "boot": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
            "start": Path(f"/proc/{pid}/stat").read_text().split(") ", 1)[1].split()[19]}


@contextmanager
def locked(directory):
    with (directory / "operator.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        path = directory / "operator.json"
        record = json.loads(path.read_text())
        if record.get("schema_version") != 1 or not isinstance(record.get("events"), list):
            raise Stop("invalid_input", "The operator record is unreadable")
        yield record
        durable_json(path, record)


def create(directory):
    path = directory / "operator.json"
    if path.exists():
        raise Stop("invalid_input", "An operator record already exists")
    durable_json(path, {"schema_version": 1, "controller": process_identity(os.getpid()), "closed": False, "events": [], "consumed": []})


def close(directory):
    with locked(directory) as record:
        record["closed"] = True


def check_stop(directory):
    with locked(directory) as record:
        if any(e["action"] == "stop" for e in record["events"]):
            raise Stop("cancelled", "The operator requested a stop")


def append(directory, action, message=None, actor="operator"):
    if action not in STATES | {"check-in", "feedback", "stop"} or actor not in ("operator", "supplied-test"):
        raise Stop("invalid_input", "Unknown operator action or source")
    if action == "feedback" and (not isinstance(message, str) or not 1 <= len(message.encode()) <= 2000):
        raise Stop("invalid_input", "A correction must contain 1 to 2000 bytes")
    if action != "feedback" and message is not None:
        raise Stop("invalid_input", "Only feedback accepts a message")
    with locked(directory) as record:
        if action == "feedback" and record["closed"]:
            raise Stop("invalid_input", "The run ended; no new correction can be delivered")
        if len(record["events"]) >= 100:
            raise Stop("budget_exhausted", "The operator event limit was reached")
        event = {"id": len(record["events"]) + 1, "action": action, "actor": actor, "at": now(),
                 "phase": "review" if record["closed"] else "run",
                 "monotonic": time.monotonic(), "boot": process_identity(os.getpid())["boot"]}
        if message is not None:
            event["message"] = message
        record["events"].append(event)
    if action == "stop":
        with locked(directory) as record:
            controller = record["controller"]
            try:
                if not record["closed"] and process_identity(controller["pid"]) == controller:
                    os.kill(controller["pid"], signal.SIGTERM)
            except (ProcessLookupError, FileNotFoundError):
                pass
    return event


def take_feedback(directory):
    with locked(directory) as record:
        if any(e["action"] == "stop" for e in record["events"]):
            raise Stop("cancelled", "The operator requested a stop")
        pending = [e for e in record["events"] if e["action"] == "feedback" and e["id"] not in record["consumed"]]
        if pending:
            event = pending[0]
            record["consumed"].append(event["id"])
            return event
    return None


def summary(directory, record=None):
    record = json.loads((directory / "operator.json").read_text()) if record is None else record
    events = [e for e in record["events"] if e["actor"] == "operator"]
    totals = {name: 0.0 for name in STATES - {"finished"}}
    state = None
    previous = None
    intervals = 0
    uncertain = False
    for event in events:
        if event["action"] not in STATES:
            continue
        if previous and state != "finished":
            elapsed = event["monotonic"] - previous["monotonic"]
            if event["boot"] != previous["boot"] or elapsed < 0:
                uncertain = True
            elif state is not None:
                totals[state] += elapsed
                intervals += 1
        state, previous = event["action"], event
    complete = intervals > 0 and state == "finished" and not uncertain
    return {"check_ins": sum(e["action"] == "check-in" for e in events),
            "corrections_submitted": sum(e["action"] == "feedback" for e in events),
            "corrections_consumed": sum(e["action"] == "feedback" and e["id"] in record["consumed"] for e in events),
            "attention_seconds": round(totals["monitoring"] + totals["reviewing"], 4) if complete else None,
            "recorded_intervals_seconds": {k: round(v, 4) for k, v in totals.items()} if intervals else None,
            "timing_complete": complete, "self_reported": True,
            "coverage": "Only explicitly recorded intervals; earlier and unrecorded attention remain unknown",
            "participation": "recorded" if events else "not recorded", "state": state or "unknown"}


def main():
    parser = argparse.ArgumentParser(description="Watch a run or explicitly record supervision. No model calls.")
    parser.add_argument("run", type=Path)
    parser.add_argument("action", choices=("status", "check-in", "feedback", "stop", *sorted(STATES)))
    parser.add_argument("--message")
    args = parser.parse_args()
    try:
        if args.action != "status":
            event = append(args.run, args.action, args.message)
            print(f"Recorded {event['action']}.")
            if event["action"] == "feedback":
                print("Feedback is delivered after the current Pi conversation stops.")
        result_path = args.run / "result.json"
        if result_path.exists():
            result = json.loads(result_path.read_text())
            status = {k: result.get(k) for k in ("run_id", "outcome", "model_requests", "tool_calls")}
            status["state"] = "finished" if result.get("ended_at") else "running"
            print(json.dumps(status, indent=2))
        live = args.run / "live-messages.jsonl"
        if live.exists():
            for raw in live.read_text().splitlines()[-3:]:
                message = json.loads(raw)["message"]
                for part in message.get("content", []):
                    if part.get("type") == "text":
                        print(part["text"])
        print(json.dumps(summary(args.run), indent=2))
        return 0
    except (Stop, OSError, ValueError, KeyError, TypeError) as error:
        print(error.reason if isinstance(error, Stop) else str(error))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
