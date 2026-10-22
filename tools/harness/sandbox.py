import argparse
import json
import os
from pathlib import Path
import signal
import sys
import time

from run import REPORTS, ROOT, Run, Stop, digest, now, write_json


def runtime_details(info):
    try:
        host = info["host"]
        security = host["security"]
        controllers = host["cgroupControllers"]
        version = info["version"]["Version"]
        if host["serviceIsRemote"] is not False:
            raise Stop("blocked", "A local Podman runtime is required")
        if security["rootless"] is not True:
            raise Stop("blocked", "Podman must run rootless")
        if host["cgroupVersion"] != "v2":
            raise Stop("blocked", "Rootless resource limits require cgroups v2")
        if not isinstance(controllers, list) or not {"cpu", "memory", "pids"}.issubset(controllers):
            raise Stop("blocked", "Podman must report CPU, memory and process controllers")
        if security["seccompEnabled"] is not True:
            raise Stop("blocked", "Podman must support seccomp filtering")
        if not isinstance(version, str) or not version:
            raise ValueError("Missing Podman version")
    except (KeyError, TypeError, ValueError) as error:
        raise Stop("blocked", "Podman returned an incomplete or invalid runtime description") from error
    return {"podman_version": version, "rootless": True, "local": True,
            "cgroup_version": "v2", "controllers": controllers, "seccomp": True}


def preflight(run):
    if sys.platform != "linux":
        raise Stop("blocked", "The sandbox requires Linux")
    if os.geteuid() == 0:
        raise Stop("blocked", "Run the sandbox checks as your normal user, without sudo")
    check, output = run.command("podman-info", ["podman", "--remote=false", "info", "--format=json"], ROOT)
    if check["exit_code"] != 0:
        raise Stop("blocked", "Podman could not inspect its local runtime; see podman-info.stderr")
    try:
        info = json.loads(output)
    except (ValueError, UnicodeDecodeError) as error:
        raise Stop("blocked", "Podman did not return a JSON runtime description") from error
    run.result["runtime"] = runtime_details(info)
    run.result.update(outcome="runtime_ready",
                      reason="Podman reports the required runtime features. Container isolation is not yet demonstrated.")
    run.event("runtime_checked", runtime=run.result["runtime"])


def main():
    argparse.ArgumentParser(description="Check Podman prerequisites without executing candidate code.").parse_args()
    run = Run(REPORTS / "sandbox-preflight")
    run.deadline = run.started + 30
    run.result.update(workflow="sandbox-preflight", synthetic=False,
                      preflight_sha256=digest(Path(__file__).read_bytes()),
                      candidate_executed=False, isolation_demonstrated=False)
    run.event("run_started", workflow="sandbox-preflight")
    previous_handlers = {}
    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signum] = signal.signal(signum, run.cancel)
        preflight(run)
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Readiness check interrupted")
    except OSError as error:
        run.result.update(outcome="blocked", reason=f"Could not inspect runtime or save evidence: {error}")
    finally:
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
    return 0 if run.result["outcome"] == "runtime_ready" else 1


if __name__ == "__main__":
    raise SystemExit(main())
