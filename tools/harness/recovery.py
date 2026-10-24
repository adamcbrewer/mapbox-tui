import copy
import difflib
import fcntl
import json
import os
from pathlib import Path
import signal
import re
import tempfile
import time

from pi_run import MAX_CALLS, ModelRequestLimit, validate_live_config
from repair import RepairTools, repair_receipt_matches
from run import HERE, REPORTS, Stop, digest, now
from verify import check_evaluator, receipt_matches, source_tree

FAULTS = ("before-edit", "after-write", "during-verification", "before-receipt")


def durable_json(path, value):
    raw = (json.dumps(value, indent=2) + "\n").encode()
    descriptor, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        Path(temporary).unlink(missing_ok=True)


def process_identity(pid):
    try:
        return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
    except (OSError, IndexError):
        return None


def validate_entries(state, task):
    def fingerprint(value):
        return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value)

    if any(not isinstance(name, str) or not fingerprint(value) for name, value in state["files"].items()):
        raise ValueError("Malformed file observations")
    if not all(isinstance(record, dict) for record in state["records"]):
        raise ValueError("Malformed journal observations")
    for operation, item in state["operations"].items():
        if not isinstance(item, dict) or item.get("status") not in ("intent", "completed"):
            raise ValueError("Malformed edit record")
        args = item.get("args")
        if (not isinstance(args, dict) or set(args) != {"file", "operation_id", "expected_sha256", "old", "new"} or
                args["operation_id"] != operation or args["file"] not in task["allowed_files"] or
                not all(isinstance(args[key], str) for key in ("old", "new")) or
                not fingerprint(item.get("before_sha256")) or not fingerprint(item.get("after_sha256")) or
                args["expected_sha256"] != item["before_sha256"] or
                item.get("fingerprint") != digest(json.dumps(args, sort_keys=True).encode()) or
                item["status"] == "completed" and not isinstance(item.get("result"), dict)):
            raise ValueError("Malformed edit intent or completion")
    for number, request in enumerate(state["requests"]):
        if (not isinstance(request, dict) or request.get("name") not in
                ("harness_read", "harness_search", "harness_edit", "harness_check") or
                not isinstance(request.get("args"), dict) or request.get("status") not in ("started", "completed") or
                request["status"] == "started" and number != len(state["requests"]) - 1 or
                request["status"] == "completed" and not isinstance(request.get("result"), dict)):
            raise ValueError("Malformed tool request")
        result = request.get("result", {})
        if "read_id" in result:
            expected_id = f"r{state['run_id'][:8]}-{number + 1}"
            if (request["name"] not in ("harness_read", "harness_search") or result.get("outcome") != "ok" or
                    result["read_id"] != expected_id or result.get("file") != request["args"].get("file") or
                    not fingerprint(result.get("sha256"))):
                raise ValueError("Malformed saved read reference")
    if (state.get("live_config") != state["result"].get("live_config") or
            state["result"].get("candidate_source") == "AI" and state.get("live_config") is None):
        raise ValueError("Saved execution mode changed or is incomplete")
    if state.get("live_config") is not None:
        config = validate_live_config(state["live_config"])
        reservations = state["model_reservations"]
        if (not isinstance(reservations, list) or len(reservations) > config["maxModelRequests"] or
                state["result"]["budgets"]["model_requests"] != config["maxModelRequests"] or
                state["result"]["budgets"]["output_tokens_per_request"] != config["maxOutputTokensPerRequest"] or
                any(state["result"].get(key) != config[key] for key in ("provider", "model", "auth"))):
            raise ValueError("Invalid persisted model budget or identity")
        for number, reservation in enumerate(reservations, 1):
            if (not isinstance(reservation, dict) or reservation.get("request") != number or
                    any(reservation.get(key) != config[key] for key in ("provider", "model"))):
                raise ValueError("Malformed model reservation")
        if state["result"].get("model_requests") != len(reservations):
            raise ValueError("Model-request count does not match durable reservations")
        usage = state["result"].get("provider_usage")
        dispatched = state["result"].get("model_requests_dispatched")
        if not isinstance(usage, list) or type(dispatched) is not int or not 0 <= dispatched <= len(reservations):
            raise ValueError("Malformed model usage or dispatch observations")
        for record in usage:
            if (not isinstance(record, dict) or type(record.get("request")) is not int or
                    not 1 <= record["request"] <= len(reservations) or not isinstance(record.get("usage"), dict)):
                raise ValueError("Malformed provider usage observation")
            row = record["usage"]
            for key in ("inputTokens", "cachedInputTokens", "outputTokens", "totalTokens", "reasoningTokens"):
                value = row.get(key)
                if key == "reasoningTokens" and key in row and value is None:
                    continue
                if type(value) is not int or value < 0:
                    raise ValueError("Malformed provider token count")
    attempts = state["result"].get("attempts")
    if not isinstance(attempts, list):
        raise ValueError("Malformed verification attempts")
    for number, attempt in enumerate(attempts, 1):
        if (not isinstance(attempt, dict) or attempt.get("attempt") != number or
                not isinstance(attempt.get("verification_run_id"), str) or
                not re.fullmatch(r"[0-9a-f]{32}", attempt["verification_run_id"]) or
                not fingerprint(attempt.get("patch_sha256"))):
            raise ValueError("Malformed verification attempt")


class Journal:
    def __init__(self, run, checkout=None):
        self.run = run
        self.path = run.path / "journal.json"
        self.fault = None
        if checkout is not None:
            self.state = {"schema_version": 1, "run_id": run.path.name, "checkout": str(checkout),
                          "expires_at": time.time() + max(0, run.deadline - time.monotonic()),
                          "files": source_tree(checkout)["files"], "seeded": (checkout / "go/import.go").read_text(),
                          "requests": [], "operations": {}, "records": [], "resumes": 0,
                          "live_config": run.result.get("live_config"), "model_reservations": [],
                          "demo": run.result.get("recovery_demo", "retry"),
                          "status": "active", "result": copy.deepcopy(run.result),
                          "implementation_sha256": {name: digest((HERE / name).read_bytes()) for name in
                                                    ("recovery.py", "recover.py", "pi_run.py", "repair.py", "pi/loop.mjs", "pi/live.mjs",
                                                     "pi/setup.mjs", "pi/cli.mjs", "pi/package-lock.json")}}
            self.record("session_created", base_commit=run.task["base_commit"])
        else:
            try:
                self.state = json.loads(self.path.read_text())
                if self.state["schema_version"] != 1 or self.state["run_id"] != run.path.name:
                    raise ValueError("Wrong session identity")
                for name, kind in (("checkout", str), ("files", dict), ("seeded", str), ("requests", list),
                                   ("operations", dict), ("records", list), ("resumes", int), ("status", str),
                                   ("result", dict), ("implementation_sha256", dict)):
                    if not isinstance(self.state[name], kind):
                        raise ValueError(f"Malformed journal field: {name}")
                validate_entries(self.state, run.task)
                for name, expected in self.state["implementation_sha256"].items():
                    if digest((HERE / name).read_bytes()) != expected:
                        raise ValueError("Recovery implementation changed")
            except (OSError, ValueError, KeyError, TypeError, AttributeError, Stop) as error:
                raise Stop("incomplete", f"Session needs deliberate reconstruction: {error}") from error
        self.checkout = Path(self.state["checkout"])
        run.journal = self

    def save(self):
        self.state["result"] = copy.deepcopy(self.run.result)
        durable_json(self.path, self.state)
        durable_json(self.run.path / "result.json", self.run.result)

    def record(self, kind, **fields):
        self.state["records"].append({"sequence": len(self.state["records"]) + 1, "at": now(),
                                      "kind": kind, **fields})
        self.save()

    def checkpoint(self, location):
        if (self.run.path / "cancel.json").exists() or self.run.cancel_requested:
            raise Stop("cancelled", "Cancellation requested; partial files and evidence are retained")
        if time.monotonic() >= self.run.deadline:
            raise Stop("timeout", "The original session deadline expired; resume cannot reset it")
        if location == self.fault:
            self.record("interruption_injected", location=location)
            raise Stop("incomplete", f"Deliberate interruption at {location}; inspect the journal before resuming")

    def reconcile(self):
        if self.checkout.is_symlink() or not self.checkout.is_dir() or self.checkout.resolve() != self.checkout:
            raise Stop("incomplete", "Saved working copy is missing or linked; deliberate reconstruction is required")
        if self.checkout.is_relative_to(self.run.path) or self.run.path.is_relative_to(self.checkout):
            raise Stop("invalid_input", "Journal and working copy must have separate edit scopes")
        expected = self.state["files"].copy()
        for operation, item in self.state["operations"].items():
            name = item["args"]["file"]
            actual = self.checkout / name
            observed = digest(actual.read_bytes()) if actual.is_file() and not actual.is_symlink() else None
            if item["status"] == "intent":
                decision = ("already_applied" if observed == item["after_sha256"] else
                            "unchanged" if observed == item["before_sha256"] else "conflict")
                self.record("edit_reconciled", operation_id=operation, file=name, observed_sha256=observed,
                            before_sha256=item["before_sha256"], after_sha256=item["after_sha256"], decision=decision)
                if decision == "conflict":
                    raise Stop("conflict", "File differs from both edit hashes; human changes were preserved")
                expected[name] = observed
        actual = source_tree(self.checkout)["files"]
        if actual != expected:
            self.record("tree_conflict", observed_files=actual)
            raise Stop("conflict", "Working copy changed outside the pending edit; no edit or check was replayed")
        check_evaluator(self.run)
        if digest((self.run.path / "task.json").read_bytes()) != self.run.result["task_sha256"]:
            raise Stop("incomplete", "Saved task identity changed; deliberate reconstruction is required")
        for attempt in self.run.result["attempts"]:
            path = REPORTS / "verification/runs" / attempt["verification_run_id"] / "result.json"
            if not path.is_file() or attempt.get("receipt_sha256") != digest(path.read_bytes()):
                raise Stop("incomplete", "Verification was interrupted or its receipt changed; inspect its evidence")
            child = json.loads(path.read_text())
            if (child.get("parent_run_id") != self.run.path.name or child.get("patch_sha256") != attempt["patch_sha256"] or
                    child.get("evaluator_sha256") != self.run.result["evaluator_sha256"] or
                    not receipt_matches(child, path.parent, allow_failed=attempt["outcome"] == "repair_failed")):
                raise Stop("incomplete", "Verification evidence is unfinished or inconsistent; no check was replayed")
        return actual

    def handoff(self, problem=None):
        attempts = self.run.result["attempts"]
        last = attempts[-1] if attempts else None
        terminal = self.state["status"] not in ("active", "incomplete")
        observed_patch = None
        path = self.checkout / "go/import.go"
        if path.is_file() and not path.is_symlink() and path.resolve().is_relative_to(self.checkout):
            patch = "".join(difflib.unified_diff(self.state["seeded"].splitlines(True), path.read_text().splitlines(True),
                                               fromfile="a/go/import.go", tofile="b/go/import.go"))
            (self.run.path / "partial.patch").write_text(patch)
            observed_patch = digest(patch.encode())
        value = {"observed_patch_sha256": observed_patch, "run_id": self.run.path.name, "task": self.run.task["id"], "base_commit": self.run.task["base_commit"],
                 "candidate_sha256": self.run.result.get("patch_sha256"),
                 "attempted_change": list(self.state["operations"]), "last_observed_check": last,
                 "evidence": f"verification/runs/{last['verification_run_id']}/result.json" if last else "journal.json",
                 "unresolved_problem": problem, "next_permitted_action": "inspect; start a new bounded task" if terminal else
                 "resume: reconcile disk, journal and receipts before creating fresh Pi context",
                 "model_explanation": "See pi-session.json; model claims do not establish file or check state"}
        durable_json(self.run.path / "handoff.json", value)
        return value


class RecoveryTools(RepairTools):
    def __init__(self, run, journal):
        super().__init__(run, journal.checkout)
        self.journal = journal
        self.seeded = journal.state["seeded"]
        self.calls = len(journal.state["requests"])
        self.reads = {request["result"]["read_id"]: request["result"] for request in journal.state["requests"]
                      if request.get("status") == "completed" and request.get("name") in ("harness_read", "harness_search")
                      and "read_id" in request.get("result", {})}
        self.edits = sum(item["status"] == "completed" for item in journal.state["operations"].values())
        self.checked = bool(self.attempts) and self.edits == len(self.attempts)
        self.terminal = bool(self.attempts) and (self.attempts[-1]["outcome"] != "repair_failed" or len(self.attempts) == 2)
        for operation, item in journal.state["operations"].items():
            if item["status"] == "completed":
                self.operations[operation] = item["fingerprint"], item["result"]
        self.refresh_context()

    def refresh_context(self):
        completed = [request for request in self.journal.state["requests"] if request["status"] == "completed"]
        self.start_context["recovery"] = {**self.journal.handoff(), "used_tool_calls": self.calls,
                                         "applied_edits": self.edits, "read_references": True,
                                         "last_tool_result": completed[-1] if completed else None,
                                         "next_required_tool": "harness_check" if self.check_available() else
                                         "harness_read" if self.edit_available() else "finish"}
        if self.journal.state.get("live_config") is not None:
            self.start_context["modelRequestsUsed"] = len(self.journal.state["model_reservations"])

    def edit_arguments(self, args):
        if set(args) != {"file", "operation_id", "read_id", "old", "new"}:
            return args
        identity = args["read_id"]
        if not isinstance(identity, str) or identity not in self.reads:
            raise ValueError("Unknown read_id; read the file before editing")
        saved = self.reads[identity]
        if saved["file"] != args["file"]:
            raise ValueError("read_id belongs to a different file")
        return {key: value for key, value in args.items() if key != "read_id"} | {"expected_sha256": saved["sha256"]}

    def reserve_model(self, message):
        self.journal.checkpoint("model-request")
        config = self.journal.state.get("live_config")
        reservations = self.journal.state.get("model_reservations", [])
        if (not config or any(message.get(key) != config[key] for key in ("provider", "model")) or
                type(message.get("request")) is not int or message["request"] != len(reservations) + 1):
            raise Stop("invalid_input", "Model reservation identity or sequence changed")
        if len(reservations) >= config["maxModelRequests"]:
            raise ModelRequestLimit("Persisted model-request budget reached")
        reservation = {key: message[key] for key in ("request", "provider", "model")}
        reservations.append({**reservation, "at": now()})
        self.run.result["model_requests"] = len(reservations)
        self.journal.record("model_reserved", **reservation)
        return {"outcome": "ok", "request": message["request"]}

    def candidate(self):
        updated = self.path("go/import.go").read_text()
        patch = "".join(difflib.unified_diff(self.seeded.splitlines(True), updated.splitlines(True),
                                           fromfile="a/go/import.go", tofile="b/go/import.go"))
        durable_json(self.run.path / "candidate-identity.json", {"sha256": digest(patch.encode())})
        (self.run.path / "candidate.patch").write_text(patch)
        self.run.result["patch_sha256"] = digest(patch.encode())

    def write_edit(self, args, path, before, after):
        operation = args["operation_id"]
        item = self.journal.state["operations"].get(operation)
        if item is None:
            item = {"args": args, "fingerprint": digest(json.dumps(args, sort_keys=True).encode()),
                    "before_sha256": digest(before), "after_sha256": digest(after), "status": "intent"}
            self.journal.state["operations"][operation] = item
            self.journal.record("edit_intent", operation_id=operation, file=args["file"],
                                before_sha256=digest(before), after_sha256=digest(after))
        if item["args"] != args:
            raise Stop("conflict", "Pending edit arguments changed; no write was replayed")
        self.journal.checkpoint("before-edit")
        if digest(path.read_bytes()) != item["before_sha256"]:
            raise Stop("conflict", "File changed after intent was recorded; no write was replayed")
        with path.open("wb") as output:
            output.write(after)
            output.flush()
            os.fsync(output.fileno())
        self.journal.checkpoint("after-write")
        if digest(path.read_bytes()) != item["after_sha256"]:
            raise Stop("conflict", "Written file differs from intended content")

    def complete_edit(self, args, result):
        item = self.journal.state["operations"][args["operation_id"]]
        item.update(status="completed", result=result.copy())
        self.journal.state["files"][args["file"]] = item["after_sha256"]
        self.journal.record("edit_completed", operation_id=args["operation_id"], observed_sha256=item["after_sha256"])

    def edit(self, args, path, result):
        item = self.journal.state["operations"].get(args["operation_id"])
        if item and item["args"] != args:
            raise ValueError("Operation ID was already used with different arguments")
        if item and item["status"] == "intent" and digest(path.read_bytes()) == item["after_sha256"]:
            self.candidate()
            result.update(outcome="ok", explanation="Observed the previously written edit; no duplicate write",
                          evidence="candidate.patch", before_sha256=item["before_sha256"], sha256=item["after_sha256"],
                          replayed=True)
            self.edits += 1
            self.operations[args["operation_id"]] = item["fingerprint"], result.copy()
        else:
            result = super().edit(args, path, result)
        if result["outcome"] == "ok" and self.journal.state["operations"][args["operation_id"]]["status"] == "intent":
            self.complete_edit(args, result)
        return result

    def prepared_calls(self, demo):
        return super().prepared_calls(demo)[len(self.journal.state["requests"]):]

    def dispatch(self, name, args):
        self.journal.checkpoint("tool")
        self.journal.reconcile()
        if self.calls >= MAX_CALLS:
            raise Stop("budget_exhausted", "Persisted tool-call budget reached")
        request = {"name": name, "args": args, "status": "started"}
        self.journal.state["requests"].append(request)
        self.journal.record("tool_started", tool=name, request=self.calls + 1)
        try:
            result = super().dispatch(name, args)
            if name in ("harness_read", "harness_search") and result["outcome"] == "ok":
                identity = f"r{self.run.path.name[:8]}-{self.calls}"
                result["read_id"] = identity
                self.reads[identity] = result.copy()
            request.update(status="completed", result=result)
            if "read_id" in result:
                self.journal.record("read_observed", read_id=result["read_id"], file=result["file"], sha256=result["sha256"])
            return result
        finally:
            self.journal.save()

    def recover_pending(self):
        requests = self.journal.state["requests"]
        if not requests or requests[-1]["status"] == "completed":
            return
        request = requests[-1]
        if request["name"] != "harness_edit":
            raise Stop("incomplete", "Interrupted check/read requires deliberate reconstruction; no blind replay")
        result = {"operation_id": request["args"]["operation_id"], "file": request["args"]["file"],
                  "outcome": "rejected", "explanation": "Pending edit", "evidence": "journal.json"}
        args = self.edit_arguments(request["args"])
        path = self.path(args["file"], writable=True)
        item = self.journal.state["operations"].get(args["operation_id"])
        if item is None:
            raise Stop("incomplete", "No durable intent exists for the pending call; inspect before reconstruction")
        result = self.edit(args, path, result)
        request.update(status="completed", result=result)
        self.journal.record("pending_call_recovered", result=result)

    def check_candidate(self, result):
        self.run.recovery_checkpoint = self.journal.checkpoint
        super().check_candidate(result)

    def finish(self):
        self.journal.checkpoint("before-receipt")
        self.journal.reconcile()
        self.candidate()
        super().finish()
        if not repair_receipt_matches(self.run.result, self.run.path, REPORTS):
            raise Stop("incomplete", "Exact-patch verification evidence is incomplete")


def lock_session(directory):
    descriptor = (directory / "session.lock").open("a+")
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        descriptor.close()
        raise Stop("incomplete", "This session already has an owner; no second resume was started") from None
    return descriptor


def publication_lock(directory):
    descriptor = (directory / "publication.lock").open("a+")
    fcntl.flock(descriptor, fcntl.LOCK_EX)
    return descriptor


def request_cancel(directory):
    with publication_lock(directory):
        try:
            state = json.loads((directory / "journal.json").read_text())
            if state.get("status") in ("complete", "cancelled"):
                return False
        except (OSError, ValueError, AttributeError):
            pass
        durable_json(directory / "cancel.json", {"requested_at": now(), "run_id": directory.name})
    try:
        owner = json.loads((directory / "owner.json").read_text())
        if owner["identity"] is not None and process_identity(owner["pid"]) == owner["identity"]:
            os.kill(owner["pid"], signal.SIGTERM)
    except (OSError, ValueError, KeyError):
        pass
    return True
