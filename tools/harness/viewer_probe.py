import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid

import base64
import hashlib
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[1 if Path(__file__).parent.name == "scripts" else 2]


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def now():
    return datetime.now(timezone.utc).isoformat()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")

sys.path.insert(0, str(ROOT / "scripts"))
import control_interactions as control
import interactions as terminal

FIXTURE = b'''<gpx version="1.1" creator="harness"><trk><name>Modal import</name><trkseg>
<trkpt lat="51" lon="0"><ele>10</ele></trkpt>
<trkpt lat="51.001" lon="0.001"><ele>11</ele></trkpt>
</trkseg></trk></gpx>'''


def preflight():
    for name in ("tmux", "rsvg-convert", "fc-match"):
        if shutil.which(name) is None:
            raise FileNotFoundError(f"Missing terminal prerequisite: {name}")
    font = subprocess.run(["fc-match", "monospace"], capture_output=True, text=True, timeout=10, check=True)
    if not font.stdout.strip():
        raise OSError("No monospace font found")
    return font.stdout.strip()


def observation(operation, status, modal, points=2):
    if not modal:
        return "blocked"
    if operation["state"] == "pending" and status["document_count"] == 0:
        return "modal_import_stalled"
    if operation["state"] != "applied" or not operation["changed"]:
        return "unexpected_state"
    tracks = status["tracks"]
    if status["document_count"] != 1 or len(tracks) != 1 or len(tracks[0]["items"]) != 1:
        return "unexpected_state"
    item = tracks[0]["items"][0]
    if item["point_count"] != points or not item["visible"] or item["ref"] != operation["result"]["ref"]:
        return "unexpected_state"
    return "passed" if status["active_item"] == item["ref"] else "unexpected_state"


def probe(binary, output, width=120, height=40, key="C-k", title="Commands", points=2, ascii_mode=False):
    fixture_bytes = FIXTURE.replace(b"</trkseg>", b"".join(
        f'<trkpt lat="{51+i*.001}" lon="{i*.001}"><ele>{10+i}</ele></trkpt>'.encode()
        for i in range(2, points)) + b"</trkseg>")
    output.mkdir(parents=True)
    started = time.monotonic()
    result = {"outcome": "blocked", "human_acceptance": "pending", "model_calls": 0,
              "started_at": now(), "terminal": {"width": width, "height": height, "TERM": "xterm-256color",
              "COLORTERM": "truecolor", "NO_COLOR": None}, "cleanup": False,
              "fixture_sha256": digest(fixture_bytes)}
    control.SOCKET = terminal.SOCKET = "mb-harness-" + uuid.uuid4().hex
    control.BINARY = binary
    write_json(output / "owner.json", {"socket": terminal.SOCKET})
    mcp = None
    try:
        result["binary_sha256"] = digest(binary.read_bytes())
        result["terminal"]["font"] = preflight()
        with tempfile.TemporaryDirectory(prefix="mb-harness-", dir="/tmp") as folder:
            write_json(output / "owner.json", {"socket": terminal.SOCKET, "runtime_directory": folder})
            env = {**os.environ, **{name: folder for name in (
                "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "TMPDIR")},
                "TERM": "xterm-256color", "COLORTERM": "truecolor", "MAPBOX_ACCESS_TOKEN": "",
                "MAPBOX_TUI_CLI": folder + "/missing-mapbox"}
            env.pop("NO_COLOR", None)
            env.pop("MAPBOX_TUI_ACCEPT_BILLING", None)
            fixture = Path(folder) / "modal.gpx"
            fixture.write_bytes(fixture_bytes)
            (output / "fixture.gpx").write_bytes(fixture_bytes)
            def remote(label, *args, success=True):
                value = control.remote(env, "modal-test", *args, success=success)
                with (output / "states.jsonl").open("a") as log:
                    log.write(json.dumps({"label": label, "at": now(), "response": value}) + "\n")
                return value
            def capture(label):
                (output / (label + ".txt")).write_text(terminal.capture())
                terminal.save_svg(output / (label + ".svg"), width, height)
            try:
                missing = remote("unavailable-viewer", "status", success=False)
                assert missing["error"]["code"] == "session_not_found", missing
                command = shlex.join(["env", "-u", "NO_COLOR", "-u", "MAPBOX_TUI_ACCEPT_BILLING",
                    *(f"{key}={env[key]}" for key in ("HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
                        "XDG_RUNTIME_DIR", "TMPDIR", "TERM", "COLORTERM", "MAPBOX_ACCESS_TOKEN", "MAPBOX_TUI_CLI")),
                    str(binary), "--name", "modal-test", "--no-provider", "--no-animation", *(["--ascii"] if ascii_mode else [])])
                terminal.tmux("start-server", ";", "set-option", "-g", "exit-empty", "off",
                              ";", "set-option", "-g", "default-shell", "/bin/sh")
                terminal.tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), "exec " + command)
                terminal.wait_for(lambda s: "Free roam" in s)
                dimensions = terminal.tmux("display-message", "-p", "-t", "poc:0.0", "#{pane_width} #{pane_height}").strip()
                assert dimensions == f"{width} {height}", dimensions
                before = remote("empty", "status")
                assert before["result"]["document_count"] == 0, before
                identity = before["session_id"]
                mcp = control.MCP(binary, env=env, session=identity)
                mcp.send("initialize", {"protocolVersion": "2025-11-25",
                    "clientInfo": {"name": "viewer-calibration", "version": "1"}, "capabilities": {}})
                mcp.send("notifications/initialized", {}, notify=True)
                def load(path, request):
                    current = remote(request + "-precondition", "status")
                    reply = mcp.call("viewer_load", {"args": {"path": str(path)}, "wait": False,
                        "request_id": request, "generation": current["generation"], "expected_revision": current["revision"]})
                    (output / (request + ".json")).write_text(json.dumps(reply, indent=2) + "\n")
                    assert not reply["result"].get("isError"), reply
                    value = reply["result"]["structuredContent"]
                    assert value["state"] == "pending" and value["operation_id"] == request, value
                    return value
                failed = remote("missing-import", "load", str(Path(folder) / "missing.gpx"), success=False)
                assert failed["state"] == "failed" and failed["error"]["code"] == "import_failed" and not failed["changed"], failed
                assert remote("after-failure", "status")["result"]["document_count"] == 0
                remote("advance-revision", "camera", "animation", "--enabled=true")
                stale = remote("stale-revision", "camera", "set", "--zoom", "3", "--generation", before["generation"],
                    "--expected-revision", str(before["revision"]), success=False)
                assert stale["error"]["code"] == "revision_conflict" and not stale["changed"], stale
                terminal.tmux("send-keys", "-t", "poc:0.0", key)
                terminal.wait_for(lambda s: terminal.active_modal(s, title))
                capture("before-import")
                load(fixture, "modal-import")
                deadline = time.monotonic() + 10
                while True:
                    operation = remote("modal-import-status", "operation", "status", "modal-import")
                    if operation["state"] != "pending" or time.monotonic() >= deadline:
                        break
                    time.sleep(.05)
                status = remote("after-import", "status")["result"]
                result["outcome"] = observation(operation, status, terminal.active_modal(terminal.capture(), title), points)
                if result["outcome"] == "passed":
                    if width >= 120:
                        terminal.wait_for(lambda s: "Modal import" in s and terminal.active_modal(s, title))
                    else:
                        terminal.settled()
                        terminal.wait_for(lambda s: terminal.active_modal(s, title))
                capture("after-import")
                result["reason"] = {"passed": f"Import completed with {points} visible points and {title} still open.",
                    "modal_import_stalled": "Deliberate regression: import remained pending for 10 seconds; viewer stayed responsive with no document.",
                    "blocked": f"{title} was not open at the assertion; the intended sequence was not established.",
                    "unexpected_state": "Viewer did not reach the fixed expected state; inspect snapshots."}[result["outcome"]]
            finally:
                try:
                    if mcp is not None:
                        try:
                            mcp.close()
                        finally:
                            if mcp.p.poll() is None:
                                mcp.p.kill()
                                mcp.p.wait(timeout=10)
                finally:
                    terminal.tmux("kill-server", check=False)
                remaining = control.run(env, "session", "list", "--json")["sessions"]
                server = subprocess.run(["tmux", "-L", terminal.SOCKET, "list-sessions"], capture_output=True, timeout=10)
                result["cleanup"] = not remaining and server.returncode != 0
                if not result["cleanup"]:
                    result.update(outcome="blocked", reason="Test-owned viewer cleanup was not confirmed.")
    except KeyboardInterrupt:
        result.update(outcome="cancelled", reason="Calibration was interrupted; no passing claim.")
    except (AssertionError, OSError, subprocess.SubprocessError, KeyError, ValueError) as error:
        result.update(outcome="blocked", reason=getattr(error, "reason", str(error)))
    finally:
        result["ended_at"] = now()
        result["duration_seconds"] = round(time.monotonic() - started, 4)
        write_json(output / "result.json", result)
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--width", type=int, default=120)
    parser.add_argument("--height", type=int, default=40)
    parser.add_argument("--key", default="C-k")
    parser.add_argument("--title", default="Commands")
    parser.add_argument("--points", type=int, default=2)
    parser.add_argument("--ascii", action="store_true")
    parser.add_argument("--label", default="commands")
    args = parser.parse_args()
    def stop(signum, frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, stop)
    result = probe(args.binary, args.output, args.width, args.height, args.key, args.title, args.points, args.ascii)
    for path in sorted(args.output.iterdir()):
        if path.name != "owner.json" and path.is_file():
            print("HARNESS_VIEWER_ARTIFACT " + json.dumps({"name": args.label + "/" + path.name,
                "data": base64.b64encode(path.read_bytes()).decode()}))
    print("HARNESS_VIEWER_RESULT " + json.dumps(result))
    return 0 if result["outcome"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
