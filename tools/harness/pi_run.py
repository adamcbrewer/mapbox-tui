import argparse
import difflib
import io
import json
import os
from pathlib import Path
import re
import selectors
import signal
import subprocess
import tarfile
import tempfile
import time

from container import ContainerRun
from run import HERE, REPORTS, ROOT, Stop, apply_patch, check_test, digest, now, write_json
from sandbox import preflight
from task_inputs import materials

LIMIT = 16384
MAX_CALLS = 12


class ModelRequestLimit(Stop):
    def __init__(self, reason):
        super().__init__("budget_exhausted", reason)


def repository_instructions(task, checkout):
    expected = task.get("instructions_sha256", {})
    path = checkout / "AGENTS.md"
    if set(expected) != {"AGENTS.md"} or path.is_symlink() or not path.is_file():
        raise Stop("invalid_input", "The task must pin the approved repository AGENTS.md")
    content = path.read_bytes()
    if not content or len(content) > LIMIT or digest(content) != expected["AGENTS.md"]:
        raise Stop("invalid_input", "Repository instructions are missing, oversized or do not match the task")
    return [{"file": "AGENTS.md", "sha256": digest(content), "text": content.decode("utf-8")}]


def prepare_checkout(run, checkout):
    task = run.task
    inputs = materials(task)
    task_bytes, seed = inputs["task"], inputs["seed"]
    if digest(seed) != task["seed_sha256"]:
        raise Stop("invalid_input", "Seed identity does not match the task")
    run.result.update(task=task["id"], base_commit=task["base_commit"], task_sha256=digest(task_bytes))
    (run.path / "task.json").write_bytes(task_bytes)
    check, version = run.command("go-version", ["go", "version"], ROOT)
    if check["exit_code"] or not version.startswith(b"go version go1.27.1 "):
        raise Stop("blocked", "The prepared image must contain Go 1.27.1")
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
    for name, content in inputs["public_files"].items():
        (checkout / name).write_bytes(content)
    for name, expected in task["source_sha256"].items():
        if not (checkout / name).is_file() or digest((checkout / name).read_bytes()) != expected:
            raise Stop("invalid_input", f"Missing or unexpected source: {name}")
    run.repo_instructions = repository_instructions(task, checkout)
    run.result["repo_instructions"] = [{"file": item["file"], "sha256": item["sha256"]}
                                       for item in run.repo_instructions]
    for stage in ("baseline", "seed"):
        if stage == "seed":
            apply_patch(run, checkout, seed, "seed", task["allowed_files"])
        check, output = run.command(stage, task["command"], checkout)
        check["source_sha256"] = digest((checkout / task["allowed_files"][0]).read_bytes())
        check_test(run, stage, check, output, task)
    run.result["startup_verified"] = True
    run.event("startup_verified", expected_failure=task["failure_message"])


class Tools:
    def __init__(self, run, checkout):
        self.run, self.checkout = run, checkout
        self.calls, self.edits, self.checked = 0, 0, False
        self.operations = {}
        self.seeded = (checkout / "go/import.go").read_text()

    def edit_available(self):
        return self.edits < self.run.task["repair_attempts"] and not self.checked

    def check_available(self):
        return not self.checked

    def completed(self):
        return self.checked and self.edits == 1

    def prepared_calls(self, demo):
        return scripted_calls(self, demo)

    def check_candidate(self, result):
        check, output = self.run.command("candidate", self.run.task["command"], self.checkout)
        check["source_sha256"] = digest(self.path("go/import.go").read_bytes())
        try:
            check_test(self.run, "candidate", check, output, self.run.task)
            outcome, explanation = "passed", "The required import cases passed"
        except Stop as error:
            if error.outcome != "repair_failed":
                raise
            outcome, explanation = error.outcome, error.reason
        self.run.result.update(outcome=outcome, reason=explanation)
        result.update(outcome=outcome, explanation=explanation, evidence="candidate.stdout")

    def path(self, name, writable=False):
        allowed = self.run.task["allowed_files"] if writable else self.run.task["source_sha256"]
        if not isinstance(name, str) or name not in allowed:
            raise ValueError("File is outside the permitted scope")
        path = self.checkout / name
        if path.is_symlink() or path.resolve() != path or not path.is_file():
            raise ValueError("File must be a plain file inside the disposable copy")
        return path

    def edit_arguments(self, args):
        return args

    def write_edit(self, args, path, before, after):
        path.write_bytes(after)

    def edit(self, args, path, result):
        operation = args["operation_id"]
        fingerprint = digest(json.dumps(args, sort_keys=True).encode())
        if operation in self.operations:
            previous, saved = self.operations[operation]
            if previous != fingerprint:
                raise ValueError("Operation ID was already used with different arguments")
            result = {**saved, "replayed": True}
        else:
            if not self.edit_available():
                raise ValueError("No edit is available; follow the check and repair budget")
            before = path.read_bytes()
            if digest(before) != args["expected_sha256"]:
                raise ValueError("File changed; read its current hash before editing")
            old, new = args["old"], args["new"]
            if not isinstance(old, str) or not isinstance(new, str) or not old or \
                    max(len(old), len(new)) > LIMIT or old == new or "\0" in old + new:
                raise ValueError("Edit must be a bounded, non-empty change")
            source = before.decode()
            if source.count(old) != 1:
                raise ValueError("Edit must match exactly one location")
            updated = source.replace(old, new, 1)
            if len(updated.encode()) > 256 * 1024:
                raise ValueError("Source size limit reached")
            self.write_edit(args, path, before, updated.encode())
            patch = "".join(difflib.unified_diff(self.seeded.splitlines(True), updated.splitlines(True),
                                               fromfile="a/go/import.go", tofile="b/go/import.go"))
            (self.run.path / "candidate.patch").write_text(patch)
            self.run.result["patch_sha256"] = digest(patch.encode())
            self.edits += 1
            result.update(outcome="ok", explanation="Edited the disposable copy", evidence="candidate.patch",
                          before_sha256=digest(before), sha256=digest(path.read_bytes()))
            self.operations[operation] = fingerprint, result.copy()
        return result

    def dispatch(self, name, args):
        self.calls += 1
        if self.calls > MAX_CALLS:
            raise Stop("budget_exhausted", "Tool-call budget reached")
        if time.monotonic() >= self.run.deadline:
            raise Stop("timeout", "Run deadline reached")
        result = {"outcome": "rejected", "explanation": "Invalid tool arguments",
                  "evidence": "events.jsonl", "operation_id": None, "file": None}
        try:
            if not isinstance(args, dict):
                raise ValueError("Arguments must be an object")
            if name in ("harness_read", "harness_search"):
                keys = {"file"} if name == "harness_read" else {"file", "text"}
                if set(args) != keys:
                    raise ValueError("Unexpected or missing arguments")
                path = self.path(args["file"])
                text = path.read_text()
                result.update(file=args["file"], sha256=digest(path.read_bytes()))
                if name == "harness_read":
                    result.update(text=text[:LIMIT], truncated=len(text) > LIMIT)
                else:
                    query = args["text"]
                    if not isinstance(query, str) or not 1 <= len(query) <= 200:
                        raise ValueError("Search text must contain 1 to 200 characters")
                    matches = [{"line": n, "text": line[:200]} for n, line in enumerate(text.splitlines(), 1)
                               if query in line]
                    result.update(matches=matches[:20], truncated=len(matches) > 20)
                result.update(outcome="ok", explanation="Read from the disposable copy")
            elif name == "harness_edit":
                args = self.edit_arguments(args)
                if set(args) != {"file", "operation_id", "expected_sha256", "old", "new"}:
                    raise ValueError("Unexpected or missing edit arguments")
                operation = args["operation_id"]
                if not isinstance(operation, str) or not re.fullmatch(r"[a-zA-Z0-9_-]{1,64}", operation):
                    raise ValueError("Invalid operation ID")
                result.update(operation_id=operation, file=args["file"])
                path = self.path(args["file"], writable=True)
                result = self.edit(args, path, result)
            elif name == "harness_check":
                if args != {"preset": "xml-leading-bom"} or not self.check_available():
                    raise ValueError("No xml-leading-bom check is available")
                self.checked = True
                for file, expected in self.run.task["source_sha256"].items():
                    path = self.path(file)
                    if file not in self.run.task["allowed_files"] and digest(path.read_bytes()) != expected:
                        raise Stop("invalid_input", "A protected test file changed")
                self.check_candidate(result)
            else:
                raise ValueError("Unknown tool; shell and default Pi tools are unavailable")
        except (ValueError, UnicodeError) as error:
            result.update(outcome="rejected", explanation=str(error))
        self.run.event("tool_result", tool=name, arguments=args, result=result)
        return result


def scripted_calls(tools, demo):
    marker = "\t\td := xml.NewDecoder(reader)"
    insert = "\n".join(line[1:] for line in (HERE / "fixtures/good.patch").read_text().splitlines()
                       if line.startswith("+") and not line.startswith("+++")) + "\n"
    old, new = (marker, insert + marker) if demo == "good" else ('"FIT"', '"fit"')
    edit = {"file": "go/import.go", "operation_id": "repair-1", "expected_sha256": digest(tools.seeded.encode()),
            "old": old, "new": new}
    return [{"name": "harness_read", "args": {"file": "go/import.go"}},
            {"name": "harness_search", "args": {"file": "go/import_edge_test.go", "text": tools.run.task["test"]}},
            {"name": "harness_read", "args": {"file": "/etc/passwd"}},
            {"name": "harness_edit", "args": edit},
            {"name": "harness_edit", "args": edit},
            {"name": "harness_check", "args": {"preset": "xml-leading-bom"}}]


def load_live_config():
    return validate_live_config(json.loads((HERE / "pi/config.local.json").read_text()))


def validate_live_config(config):
    if not isinstance(config, dict) or set(config) != {
            "provider", "model", "auth", "maxModelRequests", "maxOutputTokensPerRequest"}:
        raise Stop("invalid_input", "Use the five preferences in pi/config.example.json")
    if (config["auth"] != "subscription" or config["provider"] != "openai" or
            not isinstance(config["model"], str) or not config["model"].strip()):
        raise Stop("invalid_input", "Choose provider openai, subscription auth and an exact model")
    for name, maximum in (("maxModelRequests", 12), ("maxOutputTokensPerRequest", 8192)):
        if name == "maxOutputTokensPerRequest" and config[name] is None:
            continue
        if type(config[name]) is not int or not 1 <= config[name] <= maximum:
            raise Stop("invalid_input", f"{name} must be an integer from 1 to {maximum}")
    return config


def pi_loop(run, tools, temporary, demo, config=None):
    package = HERE / "pi"
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(temporary),
           "PI_OFFLINE": "1", "PI_CODING_AGENT_DIR": str(temporary / "agent")}
    initial_calls = tools.calls
    payload = {"type": "start", "task": run.task, "maxToolCalls": MAX_CALLS - initial_calls}
    payload["repoInstructions"] = getattr(run, "repo_instructions", [])
    payload.update(getattr(tools, "start_context", {}))
    permissions = []
    if config:
        if not payload["repoInstructions"]:
            raise Stop("invalid_input", "Approved repository instructions are required before model work")
        auth_dir = Path.home() / ".pi/agent"
        if not (auth_dir / "auth.json").is_file():
            raise Stop("blocked", "Pi subscription sign-in is missing; run pi/setup.mjs login")
        permissions = ["--allow-net", "--allow-fs-read=" + str(auth_dir), "--allow-fs-write=" + str(auth_dir)]
        payload.update(mode="live", config=config, authDir=str(auth_dir))
    else:
        payload["calls"] = tools.prepared_calls(demo)
    with (run.path / "pi.stderr").open("wb") as stderr:
        process = None
        try:
            run.launching = True
            try:
                process = subprocess.Popen(["node", "--permission", "--allow-fs-read=" + str(package),
                                            "--allow-fs-read=" + str(temporary), "--allow-fs-write=" + str(temporary),
                                            *permissions,
                                            str(package / "cli.mjs")], cwd=temporary, env=env,
                                           stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr,
                                           start_new_session=True)
            finally:
                run.launching = False
            if run.cancel_requested:
                raise KeyboardInterrupt
            process.stdin.write((json.dumps(payload) + "\n").encode())
            process.stdin.flush()
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                buffer = b""
                while True:
                    remaining = run.deadline - time.monotonic()
                    if remaining <= 0 or not selector.select(remaining):
                        raise Stop("timeout", "Pi tool-loop deadline reached")
                    chunk = os.read(process.stdout.fileno(), 65536)
                    if not chunk:
                        raise Stop("blocked", "Pi exited before completing its loop; see pi.stderr")
                    buffer += chunk
                    if len(buffer) > 2 * 1024 * 1024:
                        raise Stop("invalid_input", "Pi protocol message exceeds its limit")
                    while b"\n" in buffer:
                        line, buffer = buffer.split(b"\n", 1)
                        message = json.loads(line)
                        if not isinstance(message, dict):
                            raise Stop("invalid_input", "Pi protocol requires an object")
                        message_type = message.get("type")
                        if message_type == "context":
                            write_json(run.path / "context.json", message)
                        elif message_type == "model_reserve":
                            if not config or not hasattr(tools, "reserve_model") or not isinstance(message.get("id"), str):
                                raise Stop("invalid_input", "Unexpected model reservation")
                            result = tools.reserve_model(message)
                            process.stdin.write((json.dumps({"id": message["id"], "result": result}) + "\n").encode())
                            process.stdin.flush()
                        elif message_type in ("model_request", "model_response", "model_payload", "model_payload_rejected", "model_dispatch", "model_usage"):
                            run.event(message_type, **{key: value for key, value in message.items() if key != "type"})
                            if message_type == "model_request":
                                run.result["model_requests"] = message["request"]
                            elif message_type == "model_response":
                                run.result.setdefault("model_usage", []).append(message)
                            elif message_type == "model_dispatch":
                                run.result["model_requests_dispatched"] += 1
                            elif message_type == "model_usage":
                                run.result.setdefault("provider_usage", []).append(message)
                            if getattr(run, "journal", None):
                                run.journal.save()
                        elif message_type == "session":
                            write_json(run.path / "pi-session.json", message)
                        elif message_type == "failed":
                            if message.get("outcome") == "budget_exhausted" and message.get("limit") == "model_requests":
                                raise ModelRequestLimit(message.get("reason", "Model-request budget reached"))
                            raise Stop(message.get("outcome", "blocked"), message.get("reason", "Pi stopped"))
                        elif message_type == "tool":
                            if not isinstance(message.get("id"), str):
                                raise Stop("invalid_input", "Pi tool request is missing its ID")
                            result = tools.dispatch(message.get("name"), message.get("args"))
                            process.stdin.write((json.dumps({"id": message["id"], "result": result}) + "\n").encode())
                            process.stdin.flush()
                        elif message_type == "finished":
                            write_json(run.path / "pi-session.json", message)
                            if getattr(tools, "start_context", None):
                                claims = [part.get("text", "") for item in message.get("messages", []) if item.get("role") == "assistant"
                                          for part in item.get("content", []) if part.get("type") == "text"]
                                run.result["model_claim"] = "\n".join(claims)[-2000:] or None
                            if process.wait(timeout=max(0.1, run.deadline - time.monotonic())) != 0:
                                raise Stop("blocked", "Pi reported a failed exit")
                            expected = tools.calls - initial_calls if config else len(payload["calls"])
                            if (message.get("toolResults") == expected and config and
                                    run.result["model_requests"] == config["maxModelRequests"] and tools.check_available()):
                                raise ModelRequestLimit("Model-request budget reached with a saved edit awaiting verification")
                            if not tools.checked or message.get("toolResults") != expected or (config and not tools.completed()):
                                raise Stop("incomplete", "Pi did not finish the required tool sequence")
                            return
                        else:
                            raise Stop("invalid_input", "Unknown Pi protocol message")
        finally:
            if process is not None:
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                process.wait()
                process.stdin.close()
                process.stdout.close()


def token_summary(records, dispatched):
    by_request = {item["request"]: item["usage"] for item in records}
    usage = list(by_request.values())
    totals = {name: sum(row[name] for row in usage) for name in
              ("inputTokens", "cachedInputTokens", "outputTokens", "totalTokens")}
    totals["reasoningTokens"] = (sum(row["reasoningTokens"] for row in usage)
                                if all(row["reasoningTokens"] is not None for row in usage) else None)
    return {"requests_with_usage": len(usage), "requests_without_usage": max(0, dispatched - len(usage)),
            "complete": len(usage) == dispatched, "totals": totals,
            "largest_response_output_tokens": max((row["outputTokens"] for row in usage), default=0),
            "responses_over_4096_output_tokens": sum(row["outputTokens"] > 4096 for row in usage)}


def main():
    parser = argparse.ArgumentParser(description="Run controlled Pi tools with prepared replies or a subscription model.")
    parser.add_argument("--image", required=True, help="Immutable prepared local image ID")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--demo", choices=("good", "wrong", "retry", "exhausted"))
    mode.add_argument("--live", action="store_true", help="Use the subscription and limits in pi/config.local.json")
    parser.add_argument("--verify", action="store_true", help="Independently verify each repair, with at most one retry")
    args = parser.parse_args()
    from repair import RepairRun, RepairTools
    run = RepairRun(REPORTS / "repair", args.image) if args.verify else ContainerRun(REPORTS / "pi", args.image)
    run.result.update(workflow="pi-verified-repair" if args.verify else "pi-live-tools" if args.live else "pi-scripted-tools",
                      candidate="model" if args.live else args.demo, pi_version="1.2.0",
                      model=None if args.live else "scripted (no AI)", startup_verified=False, human_acceptance="pending",
                      budgets={"tool_calls": MAX_CALLS, "repair_attempts": 2 if args.verify else 1, "deadline_seconds": 600,
                               "model_requests": 0},
                      source_sha256={str(path.relative_to(ROOT)): digest(path.read_bytes()) for path in
                                     [Path(__file__), HERE / "pi/loop.mjs", HERE / "pi/live.mjs",
                                      HERE / "pi/setup.mjs", HERE / "pi/cli.mjs", HERE / "pi/package-lock.json"]})
    if args.verify:
        run.result["source_sha256"].update({str(path.relative_to(ROOT)): digest(path.read_bytes()) for path in
                                            (HERE / "repair.py", HERE / "verify.py", HERE / "run.py")})
    if args.live:
        run.result.update(candidate_source="AI", model_cost_usd=None, model_requests=0,
                          model_requests_dispatched=0, model_usage=[])
        run.result["budgets"]["model_requests"] = None
    previous = {signum: signal.signal(signum, run.cancel) for signum in (signal.SIGINT, signal.SIGTERM)}
    try:
        if args.demo and ((args.demo in ("retry", "exhausted")) != args.verify):
            raise Stop("invalid_input", "Use --verify with --demo retry or exhausted; omit it for good or wrong")
        config = load_live_config() if args.live else None
        if config:
            run.result.update(provider=config["provider"], model=config["model"], auth=config["auth"], reasoning="low")
            run.result["budgets"].update(model_requests=config["maxModelRequests"],
                                         output_tokens_per_request=config["maxOutputTokensPerRequest"], retries=0)
        write_json(run.path / "result.json", run.result)
        preflight(run)
        run.result["outcome"] = "incomplete"
        run.prepare()
        with tempfile.TemporaryDirectory(prefix="mapbox-calibration-") as source, \
                tempfile.TemporaryDirectory(prefix="mapbox-pi-") as agent:
            checkout = Path(source)
            prepare_checkout(run, checkout)
            tools = RepairTools(run, checkout) if args.verify else Tools(run, checkout)
            pi_loop(run, tools, Path(agent), args.demo, config)
            run.result["tool_calls"] = tools.calls
            if args.verify:
                tools.finish()
            elif run.result["outcome"] == "passed":
                run.result.update(outcome="live_repair_passed" if config else "scripted_loop_passed",
                                  reason="The AI-written repair passed the fixed import check; human review remains pending" if config else
                                  "Prepared responses exercised Pi tools and the sandboxed check; no AI ran")
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Pi tool loop interrupted")
    except (OSError, ValueError, subprocess.TimeoutExpired, tarfile.TarError) as error:
        run.result.update(outcome="blocked", reason=f"Cannot complete the Pi tool loop: {error}")
    finally:
        run.finalizing = True
        if "tools" in locals():
            run.result["tool_calls"] = tools.calls
        if args.live:
            run.result["token_summary"] = token_summary(run.result.get("provider_usage", []),
                                                         run.result["model_requests_dispatched"])
        run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
        write_json(run.path / "result.json", run.result)
        run.event("run_finished", outcome=run.result["outcome"])
        for signum, handler in previous.items():
            signal.signal(signum, handler)
    print(f"{run.result['outcome']}: {run.result.get('reason', 'See evidence')}")
    print(run.path / "result.json")
    return 0 if run.result["outcome"] in ("scripted_loop_passed", "live_repair_passed", "verified_repair_passed") else 1


if __name__ == "__main__":
    raise SystemExit(main())
