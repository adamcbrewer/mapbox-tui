import argparse
import json
from pathlib import Path
import signal
import subprocess
import tarfile
import tempfile
import time

from container import ContainerRun, REPOSITORY_GOFLAGS
from pi_run import prepare_checkout
from run import HERE, REPORTS, Stop, apply_patch, classify_test, check_test, digest, now, read_input, write_json
from sandbox import preflight

WORKFLOW = "frozen-patch-verification"
SCOPE = "repository-checks-v1"
STAGES = ("baseline", "seed", "candidate")
EXTRA_CHECKS = ("candidate-format", "candidate-suite")
SCOPES = {"focused-only-v1": (1, STAGES), SCOPE: (2, STAGES + EXTRA_CHECKS)}


def check_commands(task):
    return {**{stage: task["command"] for stage in STAGES},
            "candidate-format": ["python3", "scripts/check_format.py"],
            "candidate-suite": ["make", "test", "BUILD_VCS=false", "COMMIT=" + task["base_commit"], REPOSITORY_GOFLAGS]}


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
                           suite_version=2, trees={}, required_checks=list(STAGES + EXTRA_CHECKS), model_claim=None,
                           verifier_sha256=digest(Path(__file__).read_bytes()),
                           preparation_sha256=digest((HERE / "pi_run.py").read_bytes()))

    def event(self, kind, **fields):
        identity = {key: self.result.get(key) for key in
                    ("patch_sha256", "base_commit", "task_sha256", "verifier_sha256", "image")}
        identity.update(attempt=1, suite_version=2, verification_scope=SCOPE, phase=self.phase)
        if "check" in fields:
            check = fields["check"]
            check.update(identity, run_id=self.path.name,
                         tree_sha256=self.result.get("trees", {}).get(self.phase, {}).get("sha256"))
            check["logical_command"] = check_commands(self.task).get(check["id"], check["command"])
            check["definition_sha256"] = fingerprint(check["logical_command"])
        super().event(kind, **identity, **fields)
        write_json(self.path / "result.json", self.result)

    def command(self, name, args, cwd, env_overrides=None):
        if name in STAGES + EXTRA_CHECKS:
            if args != check_commands(self.task)[name] or env_overrides:
                raise Stop("invalid_input", "Only the fixed verification commands are permitted")
            checkout = self.checkout_path(cwd)
            self.phase = name if name in STAGES else "candidate"
            tree = source_tree(checkout)
            if name in EXTRA_CHECKS and tree != self.result["trees"]["candidate"]:
                raise Stop("invalid_input", "The candidate changed between required checks")
            self.result["trees"][self.phase] = tree
        if name in EXTRA_CHECKS:
            check, output = self.execute(name, args, cwd, build_output=name == "candidate-suite")
        else:
            check, output = super().command(name, args, cwd, env_overrides)
        if name in STAGES + EXTRA_CHECKS:
            if source_tree(Path(cwd)) != self.result["trees"][self.phase]:
                raise Stop("invalid_input", "Verification source changed while a check was running")
            check.update(stdout_sha256=digest((self.path / check["stdout"]).read_bytes()),
                         stderr_sha256=digest((self.path / check["stderr"]).read_bytes()))
        return check, output


def receipt_matches(result, directory, allow_failed=False):
    try:
        task_bytes = (directory / "task.json").read_bytes()
        task = json.loads(task_bytes)
        version, required = SCOPES[result["verification_scope"]]
        commands = check_commands(task)
        if (result["workflow"] != WORKFLOW or result["suite_version"] != version or
                result["run_id"] != directory.name or result["attempt"] != 1 or
                result["base_commit"] != task["base_commit"] or
                result["task_sha256"] != digest(task_bytes) or
                result["patch_sha256"] != digest((directory / "candidate.patch").read_bytes()) or
                task["seed_sha256"] != digest((directory / "seed.patch").read_bytes()) or
                result["required_checks"] != list(required)):
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
        checks = [check for check in result["checks"] if check["id"] in required]
        if ([check["id"] for check in checks] != list(required[:len(checks)]) or
                not checks or len(checks) > len(required) or not allow_failed and len(checks) != len(required)):
            return False
        failed = False
        for check in checks:
            name = check["id"]
            stage = name if name in STAGES else "candidate"
            if (any(check[key] != result[key] for key in
                    ("run_id", "attempt", "patch_sha256", "base_commit", "task_sha256", "verifier_sha256", "image")) or
                    check["phase"] != stage or check["verification_scope"] != result["verification_scope"] or
                    check["tree_sha256"] != trees[stage]["sha256"] or
                    check["logical_command"] != commands[name] or check["suite_version"] != version or
                    check["definition_sha256"] != fingerprint(check["logical_command"]) or
                    not check["started_at"] or not check["ended_at"] or check["duration_seconds"] < 0 or
                    type(check["exit_code"]) is not int):
                return False
            logs = {}
            for stream in ("stdout", "stderr"):
                if check[stream] != f"{name}.{stream}":
                    return False
                logs[stream] = (directory / check[stream]).read_bytes()
                if digest(logs[stream]) != check[stream + "_sha256"]:
                    return False
            actual = (classify_test(logs["stdout"], check["exit_code"], task) if name in STAGES else
                      "passed" if check["exit_code"] == 0 else "failed")
            expected = "assertion_failed" if name == "seed" else "passed"
            if check["outcome"] != actual:
                return False
            if actual != expected:
                if (not allow_failed or check is not checks[-1] or name in ("baseline", "seed") or
                        actual not in ("assertion_failed", "build_failed", "failed") or check["exit_code"] == 0):
                    return False
                failed = True
            if name == "seed" and task["failure_message"].encode() not in logs["stdout"]:
                return False
        return failed if allow_failed else True
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
        (checkout / "bin").mkdir(exist_ok=True)
        for name in EXTRA_CHECKS:
            check, output = run.command(name, check_commands(run.task)[name], checkout)
            diagnostics = (output + (run.path / check["stderr"]).read_bytes()).lower()
            if any(marker in diagnostics for marker in (
                    b"module lookup disabled by goproxy=off", b"no space left on device",
                    b"python3: not found", b"gofmt: not found", b"go: not found")):
                check["outcome"] = "blocked"
                run.event("check_classified", check=check)
                raise Stop("blocked", f"{name}: required tools, dependencies or temporary space are unavailable")
            run.event("check_classified", check=check)
            if check["exit_code"] != 0:
                raise Stop("repair_failed", f"The candidate did not pass {name}; inspect its logs")
        if not receipt_matches(run.result, run.path):
            raise Stop("invalid_input", "The patch, source identities or required check evidence changed")
    run.result.update(outcome="repository_verification_passed",
                      reason="This frozen patch passed the import, formatting and repository checks; extra independent cases and human review remain pending")
    run.event("repository_verification_completed")


def main():
    parser = argparse.ArgumentParser(description="Independently check a frozen repair in a fresh sandboxed source copy.")
    parser.add_argument("--image", required=True, help="Immutable prepared local image ID")
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--demo", choices=("good", "wrong", "unformatted", "empty"))
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
    return 0 if run.result["outcome"] == "repository_verification_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
