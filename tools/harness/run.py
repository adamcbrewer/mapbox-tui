import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
from datetime import datetime, timezone
import uuid

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
REPORTS = ROOT / "reports/harness"


def now():
    return datetime.now(timezone.utc).isoformat()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def write_json(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


class Stop(Exception):
    def __init__(self, outcome, reason):
        self.outcome, self.reason = outcome, reason


class Run:
    def __init__(self, reports):
        self.path = reports / "runs" / uuid.uuid4().hex
        self.path.mkdir(parents=True)
        self.started = time.monotonic()
        self.deadline = self.started + 600
        self.result = {
            "schema_version": 1, "run_id": self.path.name, "attempt": 1,
            "started_at": now(), "outcome": "incomplete", "checks": [],
            "synthetic": True, "human_acceptance": "pending",
            "model": None, "pi_version": None, "model_cost_usd": 0,
            "execution_cost_usd": None,
            "environment": {"python": platform.python_version(), "platform": platform.platform()},
            "runner_sha256": digest(Path(__file__).read_bytes()),
        }
        write_json(self.path / "result.json", self.result)

    def event(self, kind, **fields):
        with (self.path / "events.jsonl").open("a") as output:
            output.write(json.dumps({"schema_version": 1, "run_id": self.path.name,
                                     "event_id": uuid.uuid4().hex, "at": now(),
                                     "kind": kind, **fields}) + "\n")

    def command(self, name, args, cwd, env_overrides=None):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise Stop("timeout", "Run deadline reached")
        started = time.monotonic()
        check = {"id": name, "command": args, "started_at": now(), "exit_code": None,
                 "outcome": "incomplete", "stdout": name + ".stdout", "stderr": name + ".stderr"}
        if env_overrides:
            check["environment_overrides"] = env_overrides
        self.result["checks"].append(check)
        self.event("command_started", check=check)
        env = {**os.environ, "MAPBOX_ACCESS_TOKEN": "", "GOWORK": "off", "GOPROXY": "off",
               "GOTOOLCHAIN": "local", "GOFLAGS": "", **(env_overrides or {})}
        with (self.path / check["stdout"]).open("wb") as out, (self.path / check["stderr"]).open("wb") as err:
            try:
                try:
                    process = subprocess.Popen(args, cwd=cwd, env=env, stdout=out, stderr=err,
                                               start_new_session=True)
                except FileNotFoundError as error:
                    check["outcome"] = "blocked"
                    raise Stop("blocked", f"Missing executable: {args[0]}") from error
                try:
                    check["exit_code"] = process.wait(timeout=remaining)
                    check["outcome"] = "passed" if process.returncode == 0 else "failed"
                except (subprocess.TimeoutExpired, KeyboardInterrupt) as error:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                    check["outcome"] = "timeout" if isinstance(error, subprocess.TimeoutExpired) else "cancelled"
                    raise Stop(check["outcome"], f"Stopped during {name}") from error
            finally:
                check.update(ended_at=now(), duration_seconds=round(time.monotonic() - started, 4))
                self.event("command_finished", check=check)
        return check, (self.path / check["stdout"]).read_bytes()


def classify_test(output, exit_code, task):
    try:
        events = [json.loads(line) for line in output.splitlines() if line.strip()]
    except (ValueError, UnicodeDecodeError):
        return "blocked"
    test = task["test"]
    terminal = {event.get("Test"): event["Action"] for event in events
                if event.get("Action") in ("pass", "fail", "skip") and "Test" in event}
    if exit_code == 0 and terminal.get(test) == "pass" and all(
        terminal.get(f"{test}/{case}") == "pass" for case in task["cases"]
    ):
        return "passed"
    if exit_code != 0 and terminal.get(test) == "fail":
        return "assertion_failed"
    return "blocked"


def apply_patch(run, checkout, patch, name, allowed):
    if not patch:
        return
    path = run.path / f"{name}.patch"
    path.write_bytes(patch)
    check, output = run.command(name + "-scope", ["git", "apply", "--numstat", "-z", str(path)], checkout)
    try:
        names = [row.split(b"\t", 2)[2].decode() for row in output.split(b"\0") if row]
    except (IndexError, UnicodeDecodeError):
        names = []
    forbidden = (b"new file mode", b"deleted file mode", b"old mode", b"new mode", b"rename from", b"copy from")
    if check["exit_code"] or not names or set(names) - set(allowed) or any(x in patch for x in forbidden):
        raise Stop("invalid_input", "Patch must only edit the existing allowed source file")
    check, _ = run.command(name + "-apply", ["git", "apply", "--whitespace=error", str(path)], checkout)
    if check["exit_code"]:
        raise Stop("invalid_input", "Patch does not apply to the seeded source")


def calibrate(run, patch, fault=None):
    task_bytes = (HERE / "task.json").read_bytes()
    task = json.loads(task_bytes)
    run.result.update(task=task["id"], task_sha256=digest(task_bytes), base_commit=task["base_commit"],
                      patch_sha256=digest(patch), workflow="imports", suite_version=1)
    (run.path / "task.json").write_bytes(task_bytes)
    (run.path / "candidate.patch").write_bytes(patch)
    seed = (HERE / "fixtures/seed.patch").read_bytes()
    if digest(seed) != task["seed_sha256"]:
        raise Stop("invalid_input", "Seed identity does not match the task")
    run.deadline = run.started + task["deadline_seconds"]
    check, version = run.command("go-version", ["go", "version"], ROOT)
    if check["exit_code"]:
        raise Stop("blocked", "Go toolchain unavailable")
    run.result["environment"]["go"] = version.decode().strip()
    with tempfile.TemporaryDirectory(prefix="mapbox-calibration-") as temporary:
        checkout = Path(temporary)
        check, archive = run.command("snapshot", ["git", "archive", task["base_commit"]], ROOT)
        if check["exit_code"]:
            raise Stop("blocked", "Pinned base commit is unavailable")
        with tarfile.open(fileobj=io.BytesIO(archive)) as source:
            for member in source:
                target = checkout / member.name
                if not target.resolve().is_relative_to(checkout) or not (member.isfile() or member.isdir()):
                    raise Stop("invalid_input", "Unsupported path in source snapshot")
                if member.isfile():
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(source.extractfile(member).read())
        if fault == "missing-source":
            name = "go/import.go"
            (checkout / name).unlink()
            run.event("fault_injected", fault=fault, removed_source=name)
        for name, expected in task["source_sha256"].items():
            if not (checkout / name).is_file():
                run.result["blocker"] = "missing_source"
                raise Stop("blocked", f"Missing source: {name}")
            if digest((checkout / name).read_bytes()) != expected:
                raise Stop("invalid_input", f"Unexpected source: {name}")
        env_overrides = None
        if fault == "missing-dependency":
            env_overrides = {"GOMODCACHE": str(checkout / ".empty-module-cache")}
            run.event("fault_injected", fault=fault, environment_overrides=env_overrides)
        if fault == "timeout":
            run.deadline = min(run.deadline, time.monotonic() + 1)
            run.event("fault_injected", fault=fault, command_budget_seconds=1,
                      description="A sleeping Python command exercises the deadline; no import test runs.")
            run.command("timeout-probe", [sys.executable, "-c", "import time; print('Waiting deliberately', flush=True); time.sleep(60)"], checkout)
            raise Stop("calibration_failed", "Timeout probe unexpectedly completed")
        for stage in ("baseline", "seed", "candidate"):
            if stage == "seed":
                apply_patch(run, checkout, seed, "seed", task["allowed_files"])
            if stage == "candidate":
                apply_patch(run, checkout, patch, "candidate", task["allowed_files"])
            check, output = run.command(stage, task["command"], checkout, env_overrides)
            check["source_sha256"] = digest((checkout / "go/import.go").read_bytes())
            check["outcome"] = classify_test(output, check["exit_code"], task)
            run.event("test_classified", check=check)
            if check["outcome"] == "blocked":
                diagnostics = output + (run.path / check["stderr"]).read_bytes()
                if b"module lookup disabled by GOPROXY=off" in diagnostics:
                    run.result["blocker"] = "missing_dependency"
                    raise Stop("blocked", f"{stage}: required Go dependencies are unavailable; downloads are disabled")
                raise Stop("blocked", f"{stage}: check did not produce the required test results; inspect logs")
            if stage == "baseline" and check["outcome"] != "passed":
                raise Stop("calibration_failed", "Original behaviour did not pass")
            if stage == "seed" and (check["outcome"] != "assertion_failed" or
                                    task["failure_message"].encode() not in output):
                raise Stop("calibration_failed", "Seed did not reproduce the intended assertion failure")
            if stage == "candidate" and check["outcome"] != "passed":
                raise Stop("repair_failed", "Candidate did not restore the expected import behaviour")
    run.result.update(outcome="calibration_passed", reason="Focused calibration passed; human review pending")


def main():
    parser = argparse.ArgumentParser(description="Check a trusted patch against one synthetic import regression.")
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--demo", choices=("good", "empty", "wrong"))
    source.add_argument("--patch", type=Path, help="Trusted local patch against the seeded source")
    parser.add_argument("--fault", choices=("missing-source", "missing-dependency", "timeout"),
                        help="Deliberately prevent the check from completing, for a local demonstration")
    args = parser.parse_args()
    run = Run(REPORTS)
    run.event("run_started")
    try:
        patch = b"" if args.demo == "empty" else (
            args.patch or HERE / "fixtures" / f"{args.demo}.patch"
        ).read_bytes()
        run.result["candidate"] = args.demo or "local-patch"
        if args.fault:
            run.result["fault"] = args.fault
        calibrate(run, patch, args.fault)
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Interrupted by user")
    except (OSError, ValueError) as error:
        run.result.update(outcome="invalid_input", reason=str(error))
    finally:
        run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
        write_json(run.path / "result.json", run.result)
        run.event("run_finished", outcome=run.result["outcome"])
    print(f"{run.result['outcome']}: {run.result.get('reason', 'See evidence')}")
    print(run.path / "result.json")
    return 0 if run.result["outcome"] == "calibration_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
