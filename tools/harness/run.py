import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import selectors
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
        self.launching = False
        self.finalizing = False
        self.cancel_requested = False
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

    def cancel(self, signum, frame):
        if self.cancel_requested or self.finalizing:
            return
        self.cancel_requested = True
        if not self.launching:
            raise KeyboardInterrupt

    def event(self, kind, **fields):
        with (self.path / "events.jsonl").open("a") as output:
            output.write(json.dumps({"schema_version": 1, "run_id": self.path.name,
                                     "event_id": uuid.uuid4().hex, "at": now(),
                                     "kind": kind, **fields}) + "\n")

    def capture_output(self, process, out, err, output_limit):
        captured = 0
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ, out)
            selector.register(process.stderr, selectors.EVENT_READ, err)
            while selector.get_map():
                remaining = self.deadline - time.monotonic()
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(process.args, 0)
                ready = selector.select(remaining)
                if not ready:
                    raise subprocess.TimeoutExpired(process.args, remaining)
                # Leave room for the other open pipe, even if it isn't ready yet.
                read_size = min(65536, max(1, (output_limit - captured) // len(selector.get_map())))
                exhausted = False
                for key, _ in ready:
                    chunk = os.read(key.fd, read_size)
                    if not chunk:
                        selector.unregister(key.fileobj)
                        continue
                    available = output_limit - captured
                    key.data.write(chunk[:available])
                    captured += min(len(chunk), available)
                    if len(chunk) > available:
                        exhausted = True
                if exhausted:
                    raise Stop("budget_exhausted", f"Command output exceeded {output_limit} bytes across stdout and stderr")

    def command(self, name, args, cwd, env_overrides=None, output_limit=None):
        if sys.platform != "linux":
            raise Stop("blocked", "This harness pilot runs checks on Linux only")
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise Stop("timeout", "Run deadline reached")
        started = time.monotonic()
        check = {"id": name, "command": args, "started_at": now(), "exit_code": None,
                 "outcome": "incomplete", "stdout": name + ".stdout", "stderr": name + ".stderr"}
        if env_overrides:
            check["environment_overrides"] = env_overrides
        if output_limit is not None:
            check["output_limit_bytes"] = output_limit
        self.result["checks"].append(check)
        self.event("command_started", check=check)
        env = {**os.environ, "MAPBOX_ACCESS_TOKEN": "", "GOWORK": "off", "GOPROXY": "off",
               "GOTOOLCHAIN": "local", "GOFLAGS": "", **(env_overrides or {})}
        with (self.path / check["stdout"]).open("wb") as out, (self.path / check["stderr"]).open("wb") as err:
            process = None
            try:
                try:
                    self.launching = True
                    try:
                        process = subprocess.Popen(args, cwd=cwd, env=env,
                                                   stdout=out if output_limit is None else subprocess.PIPE,
                                                   stderr=err if output_limit is None else subprocess.PIPE,
                                                   start_new_session=True)
                    except OSError as error:
                        check["outcome"] = "blocked"
                        reason = (f"Missing executable: {args[0]}" if isinstance(error, FileNotFoundError)
                                  else f"Cannot launch {args[0]}: {error}")
                        raise Stop("blocked", reason) from error
                    finally:
                        self.launching = False
                    if self.cancel_requested:
                        raise KeyboardInterrupt
                    if output_limit is not None:
                        self.capture_output(process, out, err, output_limit)
                    remaining = self.deadline - time.monotonic()
                    if remaining <= 0:
                        raise subprocess.TimeoutExpired(args, 0)
                    check["exit_code"] = process.wait(timeout=remaining)
                    check["outcome"] = "passed" if process.returncode == 0 else "failed"
                except (subprocess.TimeoutExpired, KeyboardInterrupt) as error:
                    self.cancel_requested = True
                    check["outcome"] = "timeout" if isinstance(error, subprocess.TimeoutExpired) else "cancelled"
                    raise Stop(check["outcome"], f"Stopped during {name}") from error
                except Stop as error:
                    check["outcome"] = error.outcome
                    raise
            finally:
                if process is not None:
                    if check["exit_code"] is None:
                        try:
                            os.killpg(process.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                    process.wait()
                    if output_limit is not None:
                        process.stdout.close()
                        process.stderr.close()
                check.update(ended_at=now(), duration_seconds=round(time.monotonic() - started, 4))
                self.event("command_finished", check=check)
        return check, (self.path / check["stdout"]).read_bytes()


def classify_test(output, exit_code, task):
    if b"HARNESS_VIEWER_BLOCKED:" in output:
        return "blocked"
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
    if exit_code != 0 and any(event.get("Action") == "build-fail" or
                             event.get("FailedBuild") or
                             ("Test" not in event and "[build failed]" in event.get("Output", ""))
                             for event in events):
        return "build_failed"
    return "blocked"


def check_test(run, stage, check, output, task):
    check["outcome"] = classify_test(output, check["exit_code"], task)
    diagnostics = output + (run.path / check["stderr"]).read_bytes()
    missing_dependency = b"module lookup disabled by GOPROXY=off" in diagnostics
    if missing_dependency:
        check["outcome"] = "blocked"
    run.event("test_classified", check=check)
    if missing_dependency:
        run.result["blocker"] = "missing_dependency"
        raise Stop("blocked", f"{stage}: required Go dependencies are unavailable; downloads are disabled")
    if check["outcome"] == "build_failed":
        if stage == "candidate":
            raise Stop("repair_failed", "The proposed repair could not be built; inspect the compiler logs")
        raise Stop("blocked", f"{stage}: source could not be built; inspect the compiler logs")
    if check["outcome"] == "blocked":
        raise Stop("blocked", f"{stage}: check did not produce the required test results; inspect logs")
    if stage == "baseline" and check["outcome"] != "passed":
        raise Stop("calibration_failed", "Original behaviour did not pass")
    if stage == "seed" and (check["outcome"] != "assertion_failed" or
                            task["failure_message"].encode() not in output):
        raise Stop("calibration_failed", "Seed did not reproduce the intended assertion failure")
    if stage == "candidate" and check["outcome"] != "passed":
        raise Stop("repair_failed", "Candidate did not restore the expected behaviour")


def read_input(path):
    try:
        return path.read_bytes()
    except OSError as error:
        raise Stop("invalid_input", str(error)) from error


def apply_patch(run, checkout, patch, name, allowed, new_files=()):
    if not patch:
        return
    path = run.path / f"{name}.patch"
    path.write_bytes(patch)
    check, output = run.command(name + "-scope", ["git", "apply", "--numstat", "-z", str(path)], checkout)
    try:
        rows = [row.split(b"\t", 2) for row in output.split(b"\0") if row]
        names = [row[2].decode() for row in rows if row[0].isdigit() and row[1].isdigit()]
    except (IndexError, UnicodeDecodeError):
        names = []
    rejected = set(names) - set(allowed) - set(new_files)
    if rejected:
        run.result["rejected_paths"] = sorted(rejected)
        run.event("patch_scope_rejected", files=sorted(rejected))
    if check["exit_code"] or not names or len(names) != len(rows) or rejected:
        raise Stop("invalid_input", "Patch must only change explicitly permitted source files")
    if set(allowed) & set(new_files) or any((checkout / file).exists() or (checkout / file).is_symlink() for file in new_files):
        raise Stop("invalid_input", "Permitted new files must not already exist")
    check, metadata = run.command(name + "-metadata", ["git", "apply", "--summary", str(path)], checkout)
    creates = {f"create mode 100644 {file}" for file in set(names) & set(new_files)}
    operations = {line.strip() for line in metadata.decode().splitlines()}
    operations = {"create mode 100644 " + line.removeprefix("create ")
                  if line.startswith("create ") and not line.startswith("create mode ") else line for line in operations}
    if check["exit_code"] or operations != creates:
        raise Stop("invalid_input", "Only existing-file edits and permitted plain-file additions are allowed")
    check, _ = run.command(name + "-apply", ["git", "apply", "--whitespace=error", str(path)], checkout)
    if check["exit_code"]:
        raise Stop("invalid_input", "Patch does not apply to the seeded source")
    for file in set(names) & set(new_files):
        added = checkout / file
        if added.is_symlink() or not added.is_file() or added.stat().st_mode & 0o111:
            raise Stop("invalid_input", "New source must be a regular, non-executable file")


def calibrate(run, patch, fault=None):
    task_bytes = read_input(HERE / "task.json")
    task = json.loads(task_bytes)
    run.result.update(task=task["id"], task_sha256=digest(task_bytes), base_commit=task["base_commit"],
                      patch_sha256=digest(patch), workflow="imports", suite_version=1)
    (run.path / "task.json").write_bytes(task_bytes)
    (run.path / "candidate.patch").write_bytes(patch)
    seed = read_input(HERE / "fixtures/seed.patch")
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
            run.result["fault_injected"] = fault
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
            run.result["fault_injected"] = fault
            run.event("fault_injected", fault=fault, environment_overrides=env_overrides)
        if fault == "timeout":
            run.deadline = min(run.deadline, time.monotonic() + 1)
            run.result["fault_injected"] = fault
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
            check_test(run, stage, check, output, task)
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
    previous_handlers = {}
    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signum] = signal.signal(signum, run.cancel)
        patch = b"" if args.demo == "empty" else read_input(
            args.patch or HERE / "fixtures" / f"{args.demo}.patch"
        )
        run.result["candidate"] = args.demo or "local-patch"
        if args.fault:
            run.result["fault"] = args.fault
        calibrate(run, patch, args.fault)
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Run interrupted")
    except OSError as error:
        run.result.update(outcome="blocked", reason=f"Could not prepare or save the check: {error}")
    except ValueError as error:
        run.result.update(outcome="invalid_input", reason=str(error))
    finally:
        # Once checks stop, finish saving the decided outcome despite further signals.
        run.finalizing = True
        try:
            run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
            write_json(run.path / "result.json", run.result)
            run.event("run_finished", outcome=run.result["outcome"])
        finally:
            for signum, handler in previous_handlers.items():
                signal.signal(signum, handler)
    print(f"{run.result['outcome']}: {run.result.get('reason', 'See evidence')}")
    print(run.path / "result.json")
    return 0 if run.result["outcome"] == "calibration_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
