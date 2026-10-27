import argparse
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import tempfile
import time
import shutil
from types import SimpleNamespace

from case_suite import load_suite, validate_case
from native_check import check_candidate, prepare_verification
from native_sandbox import NativeRun, NativeSandbox
from native_worker import TIMEOUT_ERROR
from pi_run import MAX_CALLS, scripted_calls
from run import HERE, REPORTS, Stop, digest, now, write_json
from sandbox import preflight
from task_inputs import selected_task

ALLOWED = {"read": {"read", "access"}, "edit": {"read", "access", "write"},
           "write": {"mkdir", "write"}, "bash": {"bash"}}


class ToolCallLimit(Stop):
    def __init__(self):
        super().__init__("budget_exhausted", "The normal-tool call limit was reached")


class NativeTools:
    def __init__(self, run, sandbox):
        self.run, self.sandbox = run, sandbox
        self.active = None
        self.calls = 0
        self.limit = run.result.get("budgets", {}).get("tool_calls", MAX_CALLS)
        if type(self.limit) is not int or not 1 <= self.limit <= 30:
            raise Stop("invalid_input", "The normal-tool allowance must be between one and thirty")
        self.ids = set()
        self.results = []
        self.timeout_error = TIMEOUT_ERROR
        self.run.result["tool_results"] = self.results

    def dispatch(self, request):
        kind, identity = request.get("type"), request.get("toolCallId")
        if kind == "native_tool":
            if self.active is not None or not isinstance(identity, str) or identity in self.ids or request.get("name") not in ALLOWED:
                raise Stop("invalid_input", "The normal-tool sequence changed")
            if self.calls >= self.limit:
                raise ToolCallLimit()
            self.calls += 1
            self.run.result["tool_calls"] = self.calls
            self.ids.add(identity)
            self.active = identity, request["name"]
            self.run.event("native_tool_started", call=self.calls, request=request)
            return {"ok": True}
        if self.active is None or self.active[0] != identity:
            raise Stop("invalid_input", "The operation doesn't belong to an active tool")
        if kind == "native_tool_finished":
            self.results.append({"name": self.active[1], "error": request.get("error")})
            self.run.event("native_tool_finished", result=self.results[-1])
            self.active = None
            self.sandbox.capture()
            return {"ok": True}
        if kind != "native_fs" or request.get("op") not in ALLOWED[self.active[1]]:
            raise Stop("invalid_input", "That operation isn't allowed for this tool")
        operation = {key: value for key, value in request.items() if key not in ("type", "id", "toolCallId")}
        if operation["op"] == "bash":
            requested = operation.get("timeout", 120)
            if type(requested) not in (int, float) or not 0 < requested <= 600:
                return {"ok": False, "error": self.timeout_error}
            operation["timeout"] = min(requested, max(0.01, self.run.deadline - time.monotonic() - 1))
        return self.sandbox.request(operation)


def prepared_calls(run, sandbox, demo, host_file, private_file):
    if demo == "timeout":
        return [{"name": "bash", "args": {"command": "printf rejected > timeout-rejected.txt", "timeout": 120000}},
                {"name": "bash", "args": {"command": "test ! -e timeout-rejected.txt && printf 'Corrected timeout accepted\\n'", "timeout": 120}}]
    if demo == "probe":
        probe = ('import os,socket; from pathlib import Path; '
                 f'assert not Path({str(host_file)!r}).exists(); assert not Path({str(private_file)!r}).exists(); '
                 'assert os.getuid()==65534; assert os.environ["HOME"]=="/tmp"; '
                 'assert not any(k in os.environ for k in ("OPENAI_API_KEY", "MAPBOX_ACCESS_TOKEN"))\n'
                 'try:\n Path("/input/forbidden-write").write_text("no")\n raise AssertionError("Source mount was writable")\n'
                 'except OSError: pass\n'
                 'try:\n s=socket.create_connection(("1.1.1.1",53),timeout=0.5); s.close(); raise AssertionError("Network was reachable")\n'
                 'except OSError: pass\n'
                 'print("Dummy host/private files absent; source mount read-only; network denied")')
        return [{"name": "read", "args": {"path": str(host_file)}},
                {"name": "read", "args": {"path": "../private/reference.patch"}},
                {"name": "write", "args": {"path": "native-note.txt", "content": "prepared note"}},
                {"name": "edit", "args": {"path": "native-note.txt", "edits": [{"oldText": "prepared", "newText": "checked"}]}},
                {"name": "read", "args": {"path": "native-note.txt"}},
                {"name": "bash", "args": {"command": "python3 -c " + shell_quote(probe) + " && printf %s " +
                                                   shell_quote('raise RuntimeError("Project json.py must not run")\n') + " > json.py"}},
                {"name": "read", "args": {"path": "native-note.txt"}},
                {"name": "bash", "args": {"command": "rm native-note.txt json.py"}}]
    if run.task["id"] == "segment-distance-v1":
        edit = ({"old": "if j > 0 {", "new": "if j > s.Start {"} if demo == "good" else
                {"old": 'format := "FIT"', "new": 'format := "fit"'})
    elif run.task["id"] == "geometry-bounds-v1":
        edit = ({"old": "t.Bounds = [4]float64{}", "new": "t.Bounds = [4]float64{180, 90, -180, -90}"} if demo == "good" else
                {"old": 'format := "FIT"', "new": 'format := "fit"'})
    else:
        edit = scripted_calls(SimpleNamespace(seeded=sandbox.seeded, run=run), demo)[3]["args"]
    if demo == "tailored":
        marker = "\t\td := xml.NewDecoder(reader)"
        insert = "\n".join(line[1:] for line in (HERE / "fixtures/tailored.patch").read_text().splitlines()
                           if line.startswith("+") and not line.startswith("+++")) + "\n"
        edit = {"old": marker, "new": insert + marker}
    command = "cat native-note.txt && rm native-note.txt && " + " ".join(shell_quote(arg) for arg in run.task["command"][:-1])
    return [{"name": "write", "args": {"path": "native-note.txt", "content": "Prepared normal-tool check\n"}},
            {"name": "read", "args": {"path": "go/import.go"}},
            {"name": "edit", "args": {"path": "go/import.go", "edits": [{"oldText": edit["old"], "newText": edit["new"]}]}},
            {"name": "bash", "args": {"command": command}}]


def shell_quote(value):
    import shlex
    return shlex.quote(value)


def render(result):
    ready = result["outcome"] in ("native_tools_ready", "native_verification_passed", "repair_failed")
    titles = {"good": "The known fix passed its import test", "wrong": "The bad fix failed its import test, as expected",
              "probe": "The normal tools passed the boundary checks", "tailored": "The filename shortcut passed the visible test",
              "timeout": "The invalid timeout was refused and the corrected request ran"}
    title = titles[result["candidate"]] if ready else "I couldn't finish the normal-tool check"
    if result["outcome"] == "native_verification_passed":
        title = "The saved fix passed the separate checker"
    elif result["outcome"] == "repair_failed":
        title = "The separate checker caught the bad fix"
    introduction = ("I used Pi's actual read, edit, write and bash tools with supplied steps." if ready else
                    "I set out to check Pi's actual read, edit, write and bash tools with supplied steps.")
    lines = ["# " + title, "", introduction + " No repair AI ran.", "",
             "This checks the connection needed for the plain-Pi baseline in #107. It doesn't complete the AI comparison.", ""]
    if result["candidate"] == "timeout":
        lines += ["I supplied a command with timeout 120000. The runner refused it before execution and explained that "
                  "the value is in seconds, must be greater than zero and at most 600, and can be 120 for two minutes.", "",
                  "The script then supplied timeout 120. That command checked that the rejected command hadn't created its marker file, "
                  "then printed a confirmation. The first failure is expected; the corrected command should pass.", "",
                  "The script chose the correction. This doesn't establish that an AI understood the feedback or would need fewer attempts. "
                  "No Go repair or independent repair check ran in this example.", ""]
    elif result["candidate"] == "probe":
        lines += ["The supplied sequence expects host/private-file reads to be refused. Inside the container it checks that those files are absent, "
                  "the source mount is read-only, the network is unavailable and no model or Mapbox key is in the environment. "
                  "It also plants a Python module that would crash if our worker accidentally loaded code from the project.", ""]
    else:
        command = result.get("evaluator", {}).get("commands", {}).get("candidate", [])
        test = result.get("visible_test", command[command.index("-run") + 1].strip("^$") if "-run" in command else "the task's public test")
        lines += ["The supplied sequence writes a temporary note, reads the source, applies a supplied edit and runs "
                  f"`{test}`. The known fix should pass this test. The wrong edit should fail on the planted regression.", "",
                  "This is the public test Pi can run. The filename shortcut only handles the names in those visible examples. "
                  "It should pass there and fail on the extra examples with different names." if result["candidate"] == "tailored" else
                  "This is the public test Pi can run. A separate checker is needed before the repair counts.", ""]
    if not ready:
        lines += ["The attempt stopped: " + result.get("reason", "The evidence is incomplete") + ".", ""]
    lines += ["| Step | Pi tool | Result |", "| --- | --- | --- |"]
    for index, tool in enumerate(result.get("tool_results", []), 1):
        lines.append(f"| {index} | {tool['name']} | {'Reported an error or refusal' if tool['error'] else 'Completed'} |")
    if result.get("verification_requested"):
        record = result.get("verification")
        lines += ["", "## What did the separate checker find?", "",
                  "This path fixes the checking rules before starting Pi's tools. After the tools stop and their container is removed, "
                  "our existing checker can apply the exact saved patch to a fresh source copy. It shares the original ten-minute limit.", ""]
        if record:
            lines += ["| Check | Result |", "| --- | --- |"]
            labels = {"baseline": "Original code, visible examples", "seed": "Planted bug, visible examples",
                      "candidate": "Saved fix, visible examples", "baseline-regression": "Original code, extra examples",
                      "seed-regression": "Planted bug, extra examples", "candidate-regression": "Saved fix, extra examples",
                      "candidate-format": "Formatting", "candidate-suite": "Repository checks"}
            observed = {check["id"]: check for check in record["checks"]}
            for name, label in labels.items():
                check = observed.get(name)
                outcome = ("Not run" if check is None else
                           "Failed as expected" if name in ("seed", "seed-regression") and check["outcome"] == "assertion_failed" else
                           {"passed": "Passed", "assertion_failed": "Failed", "failed": "Failed",
                            "build_failed": "Couldn't build", "blocked": "Couldn't run"}.get(check["outcome"], "Not finished"))
                lines.append(f"| {label} | {outcome} |")
            lines += ["", "Read [the separate checker's saved result](../../../verification/runs/" + record["run_id"] + "/result.json). "
                      f"Its containers removed: {record['containers_removed']}/{record['containers_created']}.", ""]
        else:
            lines += ["The separate checker didn't run.", ""]
        lines += ["The repair counts as independently checked only when the patch, task, rules and all required results match. "
                  "My approval and the actual AI comparison remain separate steps.", ""]
    removed = sum(container["removed"] for container in result["containers"])
    lines += ["", f"Container removal confirmed: {removed}/{len(result['containers'])}. "
              f"Final project changes within the allowed scope: {'yes' if result.get('snapshot_scope_valid') else 'not confirmed'}.", "",
              f"Elapsed time: {result.get('duration_seconds', 'unknown')} seconds. Model calls, model tokens and model cost: zero. "
              "Execution billing and my review time: unknown. My learning review is still pending.", "",
              "Pi keeps its usual tool schemas and edit behaviour. Our code routes file operations and commands into the disposable "
              "container, limits the work, saves the proposed source change and removes the container. It doesn't treat a model's "
              "claim or a tool's own test result as independent proof.", "",
              "Read [the saved session](pi-session.json), [the result details](result.json), "
              "[the proposed patch](candidate.patch) and [the project snapshot](project-snapshot.json) if present. "
              "Keep these local files together. The [shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111) "
              "explains the wider workflow.", ""]
    return "\n".join(lines)


def session(run, tools, start, temporary):
    package = HERE / "pi"
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(temporary), "TMPDIR": str(temporary),
           "PI_OFFLINE": "1", "PI_CODING_AGENT_DIR": str(temporary / "agent")}
    process = None
    live = start.get("mode") == "live"
    permissions = (["--allow-net", "--allow-fs-read=" + start["authDir"], "--allow-fs-write=" + start["authDir"]]
                   if live else [])
    with (run.path / "pi.stderr").open("wb") as stderr:
        try:
            run.launching = True
            try:
                process = subprocess.Popen(["node", "--permission", "--allow-fs-read=" + str(package),
                                        "--allow-fs-read=/workspace/*",
                                        "--allow-fs-read=" + str(temporary), "--allow-fs-write=" + str(temporary),
                                        *permissions,
                                        str(package / "native-cli.mjs")], cwd=temporary, env=env, stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=stderr, start_new_session=True)
            finally:
                run.launching = False
            if run.cancel_requested:
                raise KeyboardInterrupt
            process.stdin.write((json.dumps(start) + "\n").encode())
            process.stdin.flush()
            finished = False
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                buffer = b""
                while True:
                    remaining = run.deadline - time.monotonic()
                    if remaining <= 0 or not selector.select(remaining):
                        raise Stop("timeout", "The normal-tool session reached its deadline")
                    chunk = os.read(process.stdout.fileno(), 65536)
                    if not chunk:
                        break
                    buffer += chunk
                    if len(buffer) > 2 * 1024 * 1024:
                        raise Stop("invalid_input", "The normal-tool response exceeded its limit")
                    while b"\n" in buffer:
                        line, buffer = buffer.split(b"\n", 1)
                        message = json.loads(line)
                        if not isinstance(message, dict):
                            raise Stop("invalid_input", "The normal-tool reply isn't an object")
                        kind = message.get("type")
                        if kind in ("native_tool", "native_tool_finished", "native_fs"):
                            result = tools.dispatch(message)
                            process.stdin.write((json.dumps({"id": message["id"], "result": result}) + "\n").encode())
                            process.stdin.flush()
                        elif kind == "model_reserve" and live:
                            result = tools.reserve_model(message)
                            process.stdin.write((json.dumps({"id": message["id"], "result": result}) + "\n").encode())
                            process.stdin.flush()
                        elif kind in ("model_request", "model_response", "model_dispatch", "model_payload", "model_payload_rejected", "model_usage") and live:
                            tools.model_event(message)
                        elif kind == "native_message" and start.get("workflowController"):
                            with (run.path / "live-messages.jsonl").open("a") as output:
                                output.write(json.dumps(message) + "\n")
                        elif kind == "workflow_checkpoint" and start.get("workflowController"):
                            result = tools.checkpoint(message)
                            process.stdin.write((json.dumps({"id": message["id"], "result": result}) + "\n").encode())
                            process.stdin.flush()
                        elif kind in ("context", "session", "finished"):
                            if kind == "context" and live:
                                tools.check_context(message)
                            write_json(run.path / ("context.json" if kind == "context" else "pi-session.json"), message)
                            finished = finished or kind == "finished"
                        elif kind == "failed":
                            raise Stop(message.get("outcome", "blocked"), message.get("reason", "The normal-tool session failed"))
                        else:
                            raise Stop("invalid_input", "Unknown normal-tool protocol message")
            try:
                code = process.wait(timeout=max(0.01, run.deadline - time.monotonic()))
            except subprocess.TimeoutExpired as error:
                raise Stop("timeout", "The normal-tool session didn't stop before its deadline") from error
            if code or not finished or tools.active or not live and not start.get("workflowController") and tools.calls != len(start["calls"]):
                raise Stop("incomplete", "The prepared normal-tool sequence didn't finish")
        finally:
            if process:
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                process.wait()
                process.stdin.close()
                process.stdout.close()


def main():
    parser = argparse.ArgumentParser(description="Check Pi's real read/edit/write/bash tools inside the sandbox. The script supplies the calls; no repair AI runs.")
    parser.add_argument("--suite", type=Path, required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--demo", choices=("good", "wrong", "tailored", "probe", "timeout"), required=True)
    parser.add_argument("--verify", action="store_true", help="Check the exact saved fix independently after Pi's tools stop")
    args = parser.parse_args()
    if args.verify and args.demo in ("probe", "timeout"):
        parser.error("--verify needs a supplied repair, not a boundary or timeout probe")
    manifest = load_suite(args.suite)
    case = next(case for case in manifest["cases"] if case["partition"] == "development")
    if case["id"] != "xml-leading-bom-v1" and args.demo == "tailored":
        parser.error("The filename-shortcut demo belongs to the XML task")
    directory = args.suite / "cases" / case["id"]
    validate_case(directory, case)
    run = NativeRun(REPORTS / "native-tools", args.image)
    run.task = selected_task(args.suite)
    run.test_command = run.task["command"]
    run.result.update(workflow="pi-native-tools", candidate=args.demo, task=case["id"], actual_model_calls=0,
                      visible_test=run.task["test"],
                      model="scripted (no AI)", pi_version="1.2.0", candidate_source="prepared", tool_results=[],
                      verification_requested=args.verify,
                      budgets={"tool_calls": MAX_CALLS, "model_requests": 0, "deadline_seconds": 600},
                      suite_sha256=digest((args.suite / "suite.json").read_bytes()),
                      source_sha256={name: digest((HERE / name).read_bytes()) for name in
                                     ("native_run.py", "native_check.py", "native_sandbox.py", "native_worker.py", "container.py", "task_inputs.py", "pi/native.mjs", "pi/native-cli.mjs", "pi/loop.mjs", "pi/package-lock.json")})
    handlers = {s: signal.signal(s, run.cancel) for s in (signal.SIGINT, signal.SIGTERM)}
    sandbox = None
    try:
        if args.verify:
            prepare_verification(run, case)
        preflight(run)
        run.prepare()
        with tempfile.TemporaryDirectory(prefix="mapbox-calibration-native-") as temporary, tempfile.TemporaryDirectory(prefix="mapbox-native-session-") as node:
            checkout, node_home = Path(temporary), Path(node)
            shutil.copytree(directory / "agent", checkout, dirs_exist_ok=True)
            host_file = node_home / "host-only.txt"
            host_file.write_text("HOST_NATIVE_PROBE")
            sandbox = NativeSandbox(run, checkout)
            try:
                sandbox.start()
                tools = NativeTools(run, sandbox)
                calls = prepared_calls(run, sandbox, args.demo, host_file, directory / "private/reference.patch")
                session(run, tools, {"task": json.loads((checkout / "task.json").read_text()), "calls": calls}, node_home)
                run.result["tool_results"] = tools.results
                run.result["tool_calls"] = tools.calls
                saved = (run.path / "pi-session.json").read_text()
                if "HOST_NATIVE_PROBE" in saved:
                    raise Stop("blocked", "The dummy host-file boundary check failed")
                expected_errors = ([True, False] if args.demo == "timeout" else
                                   [True, True, False, False, False, False, False, False] if args.demo == "probe" else
                                   [False, False, False, args.demo == "wrong"])
                if [bool(result["error"]) for result in tools.results] != expected_errors:
                    raise Stop("blocked", "The prepared normal-tool results didn't match the expected checks")
                if args.demo == "wrong" and run.task["failure_message"] not in tools.results[-1]["error"]:
                    raise Stop("blocked", "The bad fix failed for a different reason; inspect its test output")
                run.result["outcome"] = "native_tools_ready"
                run.result["reason"] = "Prepared normal tools checked; no repair AI ran"
            finally:
                sandbox.close()
                if run.result.get("snapshot_problem") or not run.result.get("snapshot_scope_valid"):
                    run.result.update(outcome="incomplete", reason="The final project snapshot needs inspection")
        if args.verify and run.result["outcome"] == "native_tools_ready":
            check_candidate(run)
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="The normal-tool check was stopped")
    except (OSError, ValueError, KeyError, TypeError) as error:
        run.result.update(outcome="blocked", reason=str(error))
    finally:
        run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
        write_json(run.path / "result.json", run.result)
        (run.path / "normal-tools.md").write_text(render(run.result))
        for signum, handler in handlers.items():
            signal.signal(signum, handler)
    print(run.result["outcome"] + ": " + run.result.get("reason", "Prepared normal tools checked; no repair AI ran"))
    print(run.path / "normal-tools.md")
    return 0 if run.result["outcome"] in ("native_tools_ready", "native_verification_passed") else 1


if __name__ == "__main__":
    raise SystemExit(main())
