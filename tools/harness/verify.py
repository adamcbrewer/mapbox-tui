import argparse
import json
from pathlib import Path
import signal
import subprocess
import tarfile
import tempfile
import time

from container import ContainerRun
from pi_run import prepare_checkout
from run import HERE, REPORTS, Stop, apply_patch, classify_test, check_test, digest, now, read_input, write_json
from sandbox import preflight

WORKFLOW = "frozen-patch-verification"
SCOPE = "focused-only-v1"
STAGES = ("baseline", "seed", "candidate")


def fingerprint(value):
    return digest(json.dumps(value, sort_keys=True, separators=(",", ":")).encode())


def source_tree(checkout):
    files = {}
    for path in sorted(checkout.rglob("*")):
        if path.is_symlink():
            raise Stop("invalid_input", "Verification source must not contain linked files")
        if path.is_file():
            files[path.relative_to(checkout).as_posix()] = digest(path.read_bytes())
    return {"sha256": fingerprint(files), "files": files}


def changed_files(before, after):
    return {name for name in before.keys() | after.keys() if before.get(name) != after.get(name)}


class VerificationRun(ContainerRun):
    def __init__(self, reports, image):
        super().__init__(reports, image)
        self.phase = "setup"
        self.result.update(workflow=WORKFLOW, verification_scope=SCOPE, fully_verified=False,
                           suite_version=1, trees={}, required_checks=list(STAGES), model_claim=None,
                           verifier_sha256=digest(Path(__file__).read_bytes()),
                           preparation_sha256=digest((HERE / "pi_run.py").read_bytes()))

    def event(self, kind, **fields):
        identity = {key: self.result.get(key) for key in
                    ("patch_sha256", "base_commit", "task_sha256", "verifier_sha256", "image")}
        identity.update(attempt=1, suite_version=1, verification_scope=SCOPE, phase=getattr(self, "phase", "setup"))
        if "check" in fields:
            check = fields["check"]
            check.update(identity, run_id=self.path.name,
                         tree_sha256=self.result.get("trees", {}).get(self.phase, {}).get("sha256"))
            check["logical_command"] = self.task["command"] if check["id"] in STAGES else check["command"]
            check["definition_sha256"] = fingerprint(check["logical_command"])
        super().event(kind, **identity, **fields)
        write_json(self.path / "result.json", self.result)

    def command(self, name, args, cwd, env_overrides=None):
        if name in STAGES:
            self.phase = name
            self.result["trees"][name] = source_tree(Path(cwd))
        check, output = super().command(name, args, cwd, env_overrides)
        if name in STAGES:
            if source_tree(Path(cwd)) != self.result["trees"][name]:
                raise Stop("invalid_input", "Verification source changed while a check was running")
            check.update(stdout_sha256=digest((self.path / check["stdout"]).read_bytes()),
                         stderr_sha256=digest((self.path / check["stderr"]).read_bytes()))
        return check, output


def receipt_matches(result, directory):
    try:
        task_bytes = (directory / "task.json").read_bytes()
        task = json.loads(task_bytes)
        if (result["workflow"] != WORKFLOW or result["verification_scope"] != SCOPE or
                result["run_id"] != directory.name or result["attempt"] != 1 or
                result["base_commit"] != task["base_commit"] or
                result["task_sha256"] != digest(task_bytes) or
                result["patch_sha256"] != digest((directory / "candidate.patch").read_bytes()) or
                task["seed_sha256"] != digest((directory / "seed.patch").read_bytes()) or
                result["required_checks"] != list(STAGES)):
            return False
        trees = result["trees"]
        for stage in STAGES:
            if trees[stage]["sha256"] != fingerprint(trees[stage]["files"]):
                return False
        for name, expected in task["source_sha256"].items():
            if trees["baseline"]["files"].get(name) != expected:
                return False
        for before, after in (("baseline", "seed"), ("seed", "candidate")):
            changes = changed_files(trees[before]["files"], trees[after]["files"])
            if not changes or changes - set(task["allowed_files"]):
                return False
        checks = [check for check in result["checks"] if check["id"] in STAGES]
        if [check["id"] for check in checks] != list(STAGES):
            return False
        for check in checks:
            stage = check["id"]
            if (any(check[key] != result[key] for key in
                    ("run_id", "attempt", "patch_sha256", "base_commit", "task_sha256", "verifier_sha256", "image")) or
                    check["phase"] != stage or check["verification_scope"] != SCOPE or
                    check["tree_sha256"] != trees[stage]["sha256"] or
                    check["logical_command"] != task["command"] or check["suite_version"] != 1 or
                    check["definition_sha256"] != fingerprint(check["logical_command"]) or
                    not check["started_at"] or not check["ended_at"] or check["duration_seconds"] < 0 or
                    type(check["exit_code"]) is not int):
                return False
            logs = {}
            for stream in ("stdout", "stderr"):
                if check[stream] != f"{stage}.{stream}":
                    return False
                logs[stream] = (directory / check[stream]).read_bytes()
                if digest(logs[stream]) != check[stream + "_sha256"]:
                    return False
            expected = "assertion_failed" if stage == "seed" else "passed"
            if check["outcome"] != expected or classify_test(logs["stdout"], check["exit_code"], task) != expected:
                return False
            if stage == "seed" and task["failure_message"].encode() not in logs["stdout"]:
                return False
        return True
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        return False


def verify(run, patch):
    task_bytes = (HERE / "task.json").read_bytes()
    run.result.update(task=run.task["id"], task_sha256=digest(task_bytes),
                      base_commit=run.task["base_commit"], patch_sha256=digest(patch))
    (run.path / "candidate.patch").write_bytes(patch)
    (run.path / "task.json").write_bytes(task_bytes)
    run.event("candidate_frozen", patch_bytes=len(patch))
    if not patch.strip() or len(patch) > 256 * 1024:
        raise Stop("invalid_input", "Supply a non-empty candidate patch of at most 256 KiB")
    preflight(run)
    run.result["outcome"] = "incomplete"
    run.prepare()
    with tempfile.TemporaryDirectory(prefix="mapbox-calibration-verify-") as temporary:
        checkout = Path(temporary)
        prepare_checkout(run, checkout)
        apply_patch(run, checkout, patch, "candidate", run.task["allowed_files"])
        seed_files = run.result["trees"]["seed"]["files"]
        candidate_files = source_tree(checkout)["files"]
        changes = changed_files(seed_files, candidate_files)
        if not changes or changes - set(run.task["allowed_files"]):
            raise Stop("invalid_input", "The candidate must change only the allowed source")
        check, output = run.command("candidate", run.task["command"], checkout)
        check_test(run, "candidate", check, output, run.task)
        if not receipt_matches(run.result, run.path):
            raise Stop("invalid_input", "The patch, source identities or required check evidence changed")
    run.result.update(outcome="focused_verification_passed",
                      reason="This frozen patch passed the focused check in a fresh copy; broader checks and human review remain pending")
    run.event("focused_verification_completed")


def main():
    parser = argparse.ArgumentParser(description="Independently check a frozen repair in a fresh sandboxed source copy.")
    parser.add_argument("--image", required=True, help="Immutable prepared local image ID")
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--demo", choices=("good", "wrong", "empty"))
    source.add_argument("--patch", type=Path, help="Candidate patch against the pinned seeded source")
    args = parser.parse_args()
    run = VerificationRun(REPORTS / "verification", args.image)
    run.result["candidate"] = args.demo or "local-patch"
    previous = {signum: signal.signal(signum, run.cancel) for signum in (signal.SIGINT, signal.SIGTERM)}
    try:
        run.event("run_started")
        patch = b"" if args.demo == "empty" else read_input(args.patch or HERE / "fixtures" / f"{args.demo}.patch")
        verify(run, patch)
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Independent verification was interrupted")
    except (OSError, ValueError, subprocess.TimeoutExpired, tarfile.TarError) as error:
        run.result.update(outcome="blocked", reason=f"Could not complete independent verification: {error}")
    finally:
        run.finalizing = True
        try:
            run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
            run.event("run_finished", outcome=run.result["outcome"])
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)
    print(f"{run.result['outcome']}: {run.result.get('reason', 'See evidence')}")
    print(run.path / "result.json")
    return 0 if run.result["outcome"] == "focused_verification_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
