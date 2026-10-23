import argparse
import json
import os
from pathlib import Path
import signal
import sys
import time

from run import HERE, REPORTS, ROOT, Run, Stop, calibrate, digest, now, read_input, write_json


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
        if not isinstance(controllers, list) or not all(isinstance(value, str) for value in controllers):
            raise ValueError("Invalid controller list")
        missing = sorted({"cpu", "memory", "pids"} - set(controllers))
        if missing:
            raise Stop("blocked", "Missing cgroup controllers: " + ", ".join(missing) +
                       ". Podman reports: " + ", ".join(controllers) +
                       ". A restricted app session may need the delegated user-unit launch in the README.")
        if security["seccompEnabled"] is not True:
            raise Stop("blocked", "Podman must support seccomp filtering")
        profile = security["seccompProfilePath"]
        if not isinstance(profile, str) or not profile.strip():
            raise ValueError("Invalid seccomp profile path")
        if not isinstance(version, str) or not version:
            raise ValueError("Missing Podman version")
    except (KeyError, TypeError, ValueError) as error:
        raise Stop("blocked", "Podman returned an incomplete or invalid runtime description") from error
    return {"podman_version": version, "rootless": True, "local": True,
            "cgroup_version": "v2", "controllers": controllers, "seccomp": True,
            "seccomp_profile": profile}


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
    parser = argparse.ArgumentParser(description="Inspect Podman, or check a prepared repair inside a restricted container.")
    parser.add_argument("--image", help="Full local image ID (sha256:<64 hex characters>); enables container calibration")
    parser.add_argument("--demo", choices=("good", "empty", "wrong"), help="Trusted prepared repair; requires --image")
    args = parser.parse_args()
    if args.demo and not args.image:
        parser.error("--demo requires --image; host execution is not a fallback")
    if args.image:
        from container import ContainerRun
        run = ContainerRun(REPORTS, args.image)
    else:
        run = Run(REPORTS / "sandbox-preflight")
    run.deadline = run.started + 30
    run.result.update(workflow="container-calibration" if args.image else "sandbox-preflight", synthetic=bool(args.image),
                      preflight_sha256=digest(Path(__file__).read_bytes()),
                      candidate_executed=False, isolation_demonstrated=False)
    run.event("run_started", workflow=run.result["workflow"])
    previous_handlers = {}
    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signum] = signal.signal(signum, run.cancel)
        preflight(run)
        if args.image:
            run.result["outcome"] = "incomplete"
            run.prepare()
            run.result["candidate"] = args.demo or "good"
            candidate = run.result["candidate"]
            patch = b"" if candidate == "empty" else read_input(HERE / "fixtures" / f"{candidate}.patch")
            calibrate(run, patch)
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
    return 0 if run.result["outcome"] in ("runtime_ready", "calibration_passed") else 1


if __name__ == "__main__":
    raise SystemExit(main())
