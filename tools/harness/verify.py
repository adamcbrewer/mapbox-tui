import argparse
import copy
import json
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tarfile
import tempfile
import time

from container import ContainerRun, ENVIRONMENT, retain_viewer_artifacts, repository_command
from pi_run import prepare_checkout
from run import HERE, REPORTS, Stop, apply_patch, classify_test, check_test, digest, now, read_input, write_json
from sandbox import preflight
from task_inputs import materials, selected_task

WORKFLOW = "frozen-patch-verification"
SCOPE = "frozen-evaluator-v1"
SUITE_VERSION = 5
STAGES = ("baseline", "seed", "candidate")
REGRESSION_CHECKS = tuple(stage + "-regression" for stage in STAGES)
EXTRA_CHECKS = ("candidate-format", "candidate-suite")
REQUIRED_CHECKS = STAGES + REGRESSION_CHECKS + EXTRA_CHECKS
SCOPES = {"focused-only-v1": (1, STAGES), "repository-checks-v1": (2, STAGES + EXTRA_CHECKS),
          "independent-cases-v1": (3, REQUIRED_CHECKS), "permitted-files-v1": (4, REQUIRED_CHECKS),
          SCOPE: (SUITE_VERSION, REQUIRED_CHECKS)}
REGRESSION_FILE = "go/import_verifier_test.go"
REGRESSION = {"test": "TestXMLImportsVerifierCases", "cases": ["gpx", "tcx", "kml"],
              "failure_message": "independent BOM import changed or failed"}


def check_commands(task):
    regression = task.get("regression", REGRESSION)
    command = ["go", "test", "./go", "-run", "^" + regression["test"] + "$", "-count=1", "-json"]
    return {**{stage: task["command"] for stage in STAGES},
            **{name: command for name in REGRESSION_CHECKS},
            "candidate-format": ["python3", "scripts/check_format.py"],
            "candidate-suite": repository_command(task)}


def fingerprint(value):
    return digest(json.dumps(value, sort_keys=True, separators=(",", ":")).encode())


def evaluator_definition(task):
    inputs = materials(task)
    return {"schema_version": 1, "verification_scope": SCOPE, "suite_version": SUITE_VERSION,
            "task_sha256": digest(inputs["task"]), "task_definition_sha256": fingerprint(task),
            "regression_sha256": digest(inputs["regression"]),
            "regression_expectations": copy.deepcopy(task.get("regression", REGRESSION)), "commands": check_commands(task),
            "required_checks": list(REQUIRED_CHECKS), "environment": dict(ENVIRONMENT),
            "source_sha256": {name: digest((HERE / name).read_bytes()) for name in
                              ("verify.py", "repair.py", "pi_run.py", "container.py", "run.py", "sandbox.py", "task_inputs.py")}}


def freeze_evaluator(run, evaluator=None):
    definition = copy.deepcopy(evaluator if evaluator is not None else evaluator_definition(run.task))
    run.result.update(evaluator=definition, evaluator_sha256=fingerprint(definition),
                      regression_sha256=definition["regression_sha256"])


def check_evaluator(run):
    if fingerprint(evaluator_definition(run.task)) != run.result["evaluator_sha256"]:
        raise Stop("invalid_input", "Verification rules changed after startup; start a new session")


def evaluator_receipt_matches(result, task):
    definition = result["evaluator"]
    return (fingerprint(definition) == result["evaluator_sha256"] and
            definition["schema_version"] == 1 and definition["verification_scope"] == result["verification_scope"] and
            definition["suite_version"] == SUITE_VERSION and definition["required_checks"] == list(REQUIRED_CHECKS) and
            definition["task_sha256"] == result["task_sha256"] and definition["task_definition_sha256"] == fingerprint(task) and
            definition["regression_expectations"] == task.get("regression", REGRESSION) and
            definition["regression_sha256"] == result["regression_sha256"] and definition["commands"] == check_commands(task))


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


def permitted_new_files(task):
    names = task.get("allowed_new_files", [])
    if (not isinstance(names, list) or any(not isinstance(name, str) or
            not re.fullmatch(r"go/[a-z][a-z0-9_]*\.go", name) or name.endswith("_test.go") for name in names) or
            len(names) != len(set(names)) or set(names) & set(task["allowed_files"])):
        raise Stop("invalid_input", "New-file permissions must name distinct non-test Go source files")
    return set(names)


def file_changes(before, after):
    return {name: {"before_sha256": before.get(name), "after_sha256": after.get(name)}
            for name in sorted(changed_files(before, after))}


class VerificationRun(ContainerRun):
    def __init__(self, reports, image, attempt=1, parent_run_id=None, evaluator=None, task=None):
        super().__init__(reports, image)
        if task is not None:
            self.task = copy.deepcopy(task)
            self.test_command = self.task["command"]
            self.deadline = self.started + self.task["deadline_seconds"]
        self.phase = "setup"
        self.sources = {}
        self.result.update(workflow=WORKFLOW, verification_scope=SCOPE, fully_verified=False,
                           attempt=attempt, parent_run_id=parent_run_id,
                           suite_version=SUITE_VERSION, trees={}, required_checks=list(REQUIRED_CHECKS), model_claim=None,
                           verifier_sha256=digest(Path(__file__).read_bytes()),
                           preparation_sha256=digest((HERE / "pi_run.py").read_bytes()))
        freeze_evaluator(self, evaluator)

    def event(self, kind, **fields):
        identity = {key: self.result.get(key) for key in
                    ("patch_sha256", "base_commit", "task_sha256", "verifier_sha256", "image")}
        identity.update(attempt=self.result["attempt"], parent_run_id=self.result.get("parent_run_id"),
                        suite_version=SUITE_VERSION, verification_scope=SCOPE, phase=self.phase,
                        evaluator_sha256=self.result["evaluator_sha256"],
                        regression_sha256=self.result.get("regression_sha256"))
        if "check" in fields:
            check = fields["check"]
            check.update(identity, run_id=self.path.name,
                         tree_sha256=self.result.get("trees", {}).get(self.phase, {}).get("sha256"))
            check["logical_command"] = check_commands(self.task).get(check["id"], check["command"])
            check["definition_sha256"] = fingerprint(check["logical_command"])
        super().event(kind, **identity, **fields)
        write_json(self.path / "result.json", self.result)

    def command(self, name, args, cwd, env_overrides=None):
        if name in REQUIRED_CHECKS:
            check_evaluator(self)
            if args != check_commands(self.task)[name] or env_overrides:
                raise Stop("invalid_input", "Only the fixed verification commands are permitted")
            checkout = self.checkout_path(cwd)
            self.phase = "candidate" if name in EXTRA_CHECKS else name
            tree = source_tree(checkout)
            if name in EXTRA_CHECKS and tree != self.result["trees"]["candidate"]:
                raise Stop("invalid_input", "The candidate changed between required checks")
            self.result["trees"][self.phase] = tree
            if name in STAGES:
                self.sources[name] = (checkout / self.task["allowed_files"][0]).read_bytes()
            if name in REGRESSION_CHECKS:
                expected = {**self.result["trees"][name.removesuffix("-regression")]["files"],
                            REGRESSION_FILE: self.result["regression_sha256"]}
                if tree["files"] != expected:
                    raise Stop("invalid_input", "Independent cases must use the exact recorded source and trusted test")
        if name in EXTRA_CHECKS + REGRESSION_CHECKS:
            check, output = self.execute(name, args, cwd, build_output=name == "candidate-suite")
        else:
            check, output = super().command(name, args, cwd, env_overrides)
        if name in REQUIRED_CHECKS:
            check_evaluator(self)
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
        parent = result.get("parent_run_id")
        if parent is not None and (not isinstance(parent, str) or not re.fullmatch(r"[0-9a-f]{32}", parent)):
            return False
        if (result["workflow"] != WORKFLOW or result["suite_version"] != version or
                result["run_id"] != directory.name or type(result["attempt"]) is not int or
                result["attempt"] not in (1, 2) or result["attempt"] == 2 and not result.get("parent_run_id") or
                result["base_commit"] != task["base_commit"] or
                result["task_sha256"] != digest(task_bytes) or
                result["patch_sha256"] != digest((directory / "candidate.patch").read_bytes()) or
                task["seed_sha256"] != digest((directory / "seed.patch").read_bytes()) or
                result["required_checks"] != list(required)):
            return False
        if version >= 5 and not evaluator_receipt_matches(result, task):
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
            new_files = permitted_new_files(task) if version >= 4 and after == "candidate" else set()
            if (not changes or changes - set(task["allowed_files"]) - new_files or
                    trees[before]["files"].keys() - trees[after]["files"].keys() or
                    new_files & trees[before]["files"].keys()):
                return False
        if version >= 4 and result["file_changes"] != file_changes(trees["seed"]["files"], trees["candidate"]["files"]):
            return False
        if version >= 3:
            regression_evidence = directory / "import_verifier_test.go.txt"
            if version == 3 and not regression_evidence.is_file():
                regression_evidence = directory / "import_verifier_test.go"
            if (REGRESSION_FILE in trees["candidate"]["files"] or
                    result["regression_sha256"] != digest(regression_evidence.read_bytes())):
                return False
            for name in REGRESSION_CHECKS:
                if name not in trees:
                    continue
                expected = {**trees[name.removesuffix("-regression")]["files"],
                            REGRESSION_FILE: result["regression_sha256"]}
                if trees[name]["files"] != expected or trees[name]["sha256"] != fingerprint(expected):
                    return False
        checks = [check for check in result["checks"] if check["id"] in required]
        if ([check["id"] for check in checks] != list(required[:len(checks)]) or
                not checks or len(checks) > len(required) or not allow_failed and len(checks) != len(required)):
            return False
        failed = False
        for check in checks:
            name = check["id"]
            stage = "candidate" if name in EXTRA_CHECKS else name
            if version >= 5 and check["evaluator_sha256"] != result["evaluator_sha256"]:
                return False
            if (any(check[key] != result[key] for key in
                    ("run_id", "attempt", "patch_sha256", "base_commit", "task_sha256", "verifier_sha256", "image")) or
                    check["phase"] != stage or check["verification_scope"] != result["verification_scope"] or
                    check.get("parent_run_id") != result.get("parent_run_id") or
                    check["tree_sha256"] != trees[stage]["sha256"] or
                    check["logical_command"] != commands[name] or check["suite_version"] != version or
                    check["definition_sha256"] != fingerprint(check["logical_command"]) or
                    not check["started_at"] or not check["ended_at"] or check["duration_seconds"] < 0 or
                    type(check["exit_code"]) is not int):
                return False
            if version >= 3 and check["regression_sha256"] != result["regression_sha256"]:
                return False
            logs = {}
            for stream in ("stdout", "stderr"):
                if check[stream] != f"{name}.{stream}":
                    return False
                logs[stream] = (directory / check[stream]).read_bytes()
                if digest(logs[stream]) != check[stream + "_sha256"]:
                    return False
            if task["id"] == "viewer-modal-v1":
                artifacts = retain_viewer_artifacts(logs["stdout"])
                if artifacts != check.get("viewer_artifacts", {}):
                    return False
                if any(digest((directory / "captures" / name / file).read_bytes()) != sha for file, sha in artifacts.items()):
                    return False
            test = task.get("regression", REGRESSION) if name in REGRESSION_CHECKS else task
            actual = (classify_test(logs["stdout"], check["exit_code"], test) if name in STAGES + REGRESSION_CHECKS else
                      "passed" if check["exit_code"] == 0 else "failed")
            expected = "assertion_failed" if name in ("seed", "seed-regression") else "passed"
            if check["outcome"] != actual:
                return False
            if actual != expected:
                if (not allow_failed or check is not checks[-1] or name not in ("candidate", "candidate-regression", *EXTRA_CHECKS) or
                        actual not in ("assertion_failed", "build_failed", "failed") or check["exit_code"] == 0):
                    return False
                failed = True
            if name in ("seed", "seed-regression") and test["failure_message"].encode() not in logs["stdout"]:
                return False
        return failed if allow_failed else True
    except (OSError, ValueError, KeyError, TypeError, AttributeError, Stop):
        return False


def verify(run, patch):
    check_evaluator(run)
    inputs = materials(run.task)
    task_bytes = inputs["task"]
    run.result.update(task=run.task["id"], task_sha256=digest(task_bytes),
                      base_commit=run.task["base_commit"], patch_sha256=digest(patch))
    regression = inputs["regression"]
    run.result["regression_sha256"] = digest(regression)
    (run.path / "import_verifier_test.go.txt").write_bytes(regression)
    (run.path / "candidate.patch").write_bytes(patch)
    (run.path / "task.json").write_bytes(task_bytes)
    run.event("candidate_frozen", patch_bytes=len(patch))
    if not patch.strip() or len(patch) > 256 * 1024:
        raise Stop("invalid_input", "Supply a non-empty candidate patch of at most 256 KiB")
    new_files = permitted_new_files(run.task)
    preflight(run)
    run.result["outcome"] = "incomplete"
    run.prepare()
    with tempfile.TemporaryDirectory(prefix="mapbox-calibration-verify-") as temporary:
        checkout = Path(temporary)
        prepare_checkout(run, checkout)
        apply_patch(run, checkout, patch, "candidate", run.task["allowed_files"], new_files)
        seed_files = run.result["trees"]["seed"]["files"]
        candidate_files = source_tree(checkout)["files"]
        changes = changed_files(seed_files, candidate_files)
        if (not changes or changes - set(run.task["allowed_files"]) - new_files or
                seed_files.keys() - candidate_files.keys()):
            raise Stop("invalid_input", "The candidate must change only the allowed source")
        run.result["file_changes"] = file_changes(seed_files, candidate_files)
        run.event("candidate_applied", file_changes=run.result["file_changes"])
        check, output = run.command("candidate", run.task["command"], checkout)
        check_test(run, "candidate", check, output, run.task)
        if getattr(run, "recovery_checkpoint", None):
            run.recovery_checkpoint("during-verification")
        if REGRESSION_FILE in candidate_files:
            raise Stop("invalid_input", "The trusted regression test path must be absent from candidate source")
        for name in REGRESSION_CHECKS:
            stage = name.removesuffix("-regression")
            with tempfile.TemporaryDirectory(prefix="mapbox-calibration-regression-") as temporary_regression:
                regression_copy = Path(temporary_regression)
                shutil.copytree(checkout, regression_copy, dirs_exist_ok=True)
                for file in candidate_files.keys() - run.result["trees"][stage]["files"].keys():
                    (regression_copy / file).unlink()
                (regression_copy / run.task["allowed_files"][0]).write_bytes(run.sources[stage])
                (regression_copy / REGRESSION_FILE).write_bytes(regression)
                check, output = run.command(name, check_commands(run.task)[name], regression_copy)
                check_test(run, stage, check, output, run.task.get("regression", REGRESSION))
        (checkout / "bin").mkdir(exist_ok=True)
        if run.task["id"] == "viewer-modal-v1":
            (checkout / "reports").mkdir(exist_ok=True)
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
                      reason="This frozen patch passed the original and independent task cases, formatting and repository checks; human review remains pending")
    run.event("repository_verification_completed")


def main():
    parser = argparse.ArgumentParser(description="Independently check a frozen repair in a fresh sandboxed source copy.")
    parser.add_argument("--image", required=True, help="Immutable prepared local image ID")
    parser.add_argument("--suite", type=Path, help="Use the registered development task from this saved suite")
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--demo", choices=("good", "wrong", "unformatted", "tailored", "new-file", "forbidden-file", "empty"))
    source.add_argument("--patch", type=Path, help="Candidate patch against the pinned seeded source")
    args = parser.parse_args()
    try:
        task = selected_task(args.suite) if args.suite else None
    except (Stop, OSError, ValueError, KeyError, TypeError) as error:
        print(error.reason if isinstance(error, Stop) else str(error))
        return 1
    run = VerificationRun(REPORTS / "verification", args.image, task=task)
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
