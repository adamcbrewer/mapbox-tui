import base64
import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import sys
import time

ROOT = Path("/workspace")
LIMIT = 1024 * 1024
TIMEOUT_ERROR = ("timeout is in seconds and must be a number greater than 0 and at most 600. "
                 "For two minutes, use 120. Omit timeout to use 120 seconds. The run's remaining time still applies.")


def project_path(value):
    if not isinstance(value, str) or not value or "\0" in value:
        raise ValueError("A project path is required")
    path = (ROOT / value).resolve()
    if not path.is_relative_to(ROOT):
        raise ValueError("That path is outside the task project")
    return path


def bash(command, timeout):
    if not isinstance(command, str) or len(command.encode()) > 65536:
        raise ValueError("The command is missing or too large")
    if type(timeout) not in (int, float) or not 0 < timeout <= 600:
        raise ValueError(TIMEOUT_ERROR)
    process = subprocess.Popen(["/bin/bash", "-c", command], cwd=ROOT, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, start_new_session=True)
    output = bytearray()
    deadline = time.monotonic() + timeout
    try:
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while selector.get_map():
                remaining = deadline - time.monotonic()
                if remaining <= 0 or not selector.select(remaining):
                    raise TimeoutError("The command reached its time limit")
                chunk = os.read(process.stdout.fileno(), 65536)
                if not chunk:
                    selector.unregister(process.stdout)
                elif len(output) + len(chunk) > LIMIT:
                    raise ValueError("The command exceeded the output limit")
                else:
                    output.extend(chunk)
        code = process.wait(timeout=max(0.01, deadline - time.monotonic()))
        return {"output": base64.b64encode(output).decode(), "exitCode": code if code >= 0 else 128 - code}
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
        process.stdout.close()


def snapshot(source_file="go/import.go"):
    files, invalid = {}, []
    source = None
    for path in sorted(ROOT.rglob("*")):
        relative = path.relative_to(ROOT)
        if relative.parts[0] in ("bin", ".git"):
            continue
        if path.is_symlink():
            invalid.append(str(relative))
        elif path.is_file():
            if path.stat().st_size > 16 * LIMIT:
                invalid.append(str(relative))
                continue
            raw = path.read_bytes()
            files[str(relative)] = hashlib.sha256(raw).hexdigest()
            if str(relative) == source_file:
                source = base64.b64encode(raw).decode()
        elif not path.is_dir():
            invalid.append(str(relative))
        if len(files) + len(invalid) > 1000:
            raise ValueError("The project exceeded the file-count limit")
    return {"files": files, "invalid": invalid, "source": source}


def dispatch(request):
    operation = request["op"]
    if operation == "snapshot":
        return snapshot(request.get("source_file", "go/import.go"))
    if operation == "bash":
        return bash(request["command"], request["timeout"])
    path = project_path(request["path"])
    if operation == "read":
        if not path.is_file() or path.stat().st_size > LIMIT:
            raise ValueError("The file is missing or exceeds the read limit")
        return {"data": base64.b64encode(path.read_bytes()).decode()}
    if operation == "access":
        flags = os.R_OK | (os.W_OK if request.get("write") else 0)
        if not path.is_file() or not os.access(path, flags):
            raise ValueError("The file isn't accessible")
    elif operation == "write":
        content = request["content"]
        if not isinstance(content, str) or len(content.encode()) > 256 * 1024:
            raise ValueError("The write exceeds the file-size limit")
        path.write_text(content)
    elif operation == "mkdir":
        path.mkdir(parents=True, exist_ok=True)
    else:
        raise ValueError("Unknown file operation")
    return {}


if __name__ == "__main__":
    try:
        result = {"ok": True, "value": dispatch(json.loads(sys.argv[1]))}
    except (OSError, ValueError, KeyError, TypeError, TimeoutError, subprocess.TimeoutExpired) as error:
        result = {"ok": False, "error": str(error)}
    print(json.dumps(result))
