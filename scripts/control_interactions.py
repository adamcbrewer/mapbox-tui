"""Exercise CLI/MCP against real Bubble Tea viewers in isolated tmux terminals."""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import json
import os
import shlex
import subprocess
import tempfile
import time

from validate import MCP

ROOT = Path(__file__).resolve().parents[1]
BINARY = ROOT / "bin/mapbox-tui"
SOCKET = f"mapbox-control-{os.getpid()}"


def tmux(*args, check=True):
    return subprocess.run(["tmux", "-L", SOCKET, "-f", "/dev/null", *args],
                          capture_output=True, text=True, check=check).stdout


def run(env, *args, success=True):
    result = subprocess.run([str(BINARY), *args], cwd=ROOT, env=env,
                            capture_output=True, text=True, timeout=15)
    assert (result.returncode == 0) == success, (args, result.stdout, result.stderr)
    assert "\x1b" not in result.stdout, result.stdout
    return json.loads(result.stdout)


def remote(env, session, *args, success=True):
    return run(env, "remote", "--session", session, *args, "--json", success=success)


def activity(session):
    return tmux("capture-pane", "-p", "-t", session).splitlines()[0]


def wait_for(predicate):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(.05)
    raise AssertionError("live control condition timed out")


def main():
    with tempfile.TemporaryDirectory(prefix="mb-control-", dir="/tmp") as folder:
        env = {**os.environ, "XDG_RUNTIME_DIR": folder, "XDG_CONFIG_HOME": folder,
               "MAPBOX_ACCESS_TOKEN": "", "TERM": "xterm-256color"}
        env.pop("NO_COLOR", None)
        def launch(name, width=120, height=40, extra=()):
            command = shlex.join(["env", f"XDG_RUNTIME_DIR={folder}",
                                 f"XDG_CONFIG_HOME={folder}", "MAPBOX_ACCESS_TOKEN=",
                                 "TERM=xterm-256color", str(BINARY), "--name", name,
                                 "--no-provider", *extra])
            tmux("new-session", "-d", "-s", name, "-x", str(width), "-y", str(height), command)
        def sessions():
            return run(env, "session", "list", "--json")["sessions"]
        try:
            launch("main")
            launch("second", 70, 24, ("--ascii",))
            records = wait_for(lambda: (r if len(r := sessions()) == 2 else None))
            identity = next(r["session_id"] for r in records if r["name"] == "main")
            duplicate = subprocess.run([str(BINARY), "--name", "main", "--no-provider"],
                                       env=env, capture_output=True, text=True, timeout=10)
            assert duplicate.returncode != 0 and "already running" in duplicate.stderr, duplicate
            assert "Remote:" not in activity("main")
            remote(env, identity, "camera", "animation", "--enabled=false")
            wait_for(lambda: "Remote: Animation updated" in activity("main"))
            remote(env, identity, "status", "--summary")
            wait_for(lambda: "Remote:" not in activity("main"))
            remote(env, identity, "theme", "set", "--theme", "nord")
            remote(env, identity, "camera", "animation", "--enabled=false")
            remote(env, identity, "camera", "set", "--zoom", "12", "--animate=false")
            expected = ["Remote: Camera updated", "Remote: Animation updated", "Remote: Theme updated"]
            def stack_visible():
                rows = tmux("capture-pane", "-p", "-t", "main").splitlines()
                return all(label in rows[index * 2] for index, label in enumerate(expected))
            wait_for(stack_visible)
            remote(env, "second", "theme", "set", "--theme", "paper")
            def ascii_activity_visible():
                text = activity("second")
                return text if "Remote: Theme updated" in text else None
            label = wait_for(ascii_activity_visible)
            assert label.isascii(), label
            # Keep a modal dialog open while remote commands change the workspace.
            tmux("send-keys", "-t", "main", "C-k")
            response = remote(env, identity, "load", "fixtures/example.gpx")
            assert response["state"] == "applied", response
            ref = response["result"]["ref"]
            ref_text = f'{ref["document_id"]}:{ref["item_id"]}'
            for action in ("play", "pause"):
                assert remote(env, identity, "playback", action, "--ref", ref_text)["state"] == "applied"
            remote(env, identity, "playback", "speed", "--ref", ref_text, "--speed", "20")
            assert remote(env, identity, "status")["result"]["playback"]["speed"] == 20
            themes = remote(env, identity, "theme", "list")["result"]["themes"]
            assert any(theme["id"] == "nord" for theme in themes), themes
            remote(env, identity, "theme", "set", "--theme", "nord")
            assert remote(env, identity, "status")["result"]["theme"] == "nord"
            animated = remote(env, identity, "camera", "set", "--lat", "51.5", "--lon", "-0.1",
                              "--zoom", "12", "--animate", "--wait-visual")
            assert animated["state"] == "applied" and animated["transition_id"], animated
            settled = wait_for(lambda: (state if not state["camera_animating"] else None)
                               if (state := remote(env, identity, "status")["result"]) else None)
            assert settled["camera_current"]["zoom"] == 12, settled
            assert remote(env, "second", "status")["result"]["tracks"] == []
            # CLI creation, MCP observation and reversal on the same session.
            created = remote(env, identity, "journey", "create", "--start", "51.5,-0.1",
                             "--end", "51.6,-0.2", "--mode", "offline")
            journey = created["result"]["ref"]
            mcp = MCP(BINARY, env=env, session=identity)
            try:
                mcp.send("initialize", {"protocolVersion": "2025-11-25",
                         "clientInfo": {"name": "live-control-test", "version": "1"}, "capabilities": {}})
                mcp.send("notifications/initialized", {}, notify=True)
                for action, args in (("viewer_theme_set", {"theme": "paper"}),
                                     ("viewer_playback_speed", {"ref": ref, "speed": 60}),
                                     ("viewer_camera_animation", {"enabled": False})):
                    current = mcp.call("viewer_status", {"args": {}})["result"]["structuredContent"]
                    updated = mcp.call(action, {"args": args, "request_id": action,
                        "generation": current["generation"], "expected_revision": current["revision"]})
                    assert not updated["result"].get("isError", False), updated
                    label = {"viewer_theme_set": "Theme updated", "viewer_playback_speed": "Playback speed updated",
                             "viewer_camera_animation": "Animation updated"}[action]
                    wait_for(lambda: f"Remote: {label}" in activity("main"))
                current = mcp.call("viewer_status", {"args": {}})["result"]["structuredContent"]
                animated = mcp.call("viewer_camera_set", {
                    "args": {"zoom": 13, "animate": True}, "wait_visual": True,
                    "request_id": "mcp-visual-wait", "generation": current["generation"],
                    "expected_revision": current["revision"]})["result"]["structuredContent"]
                assert animated["state"] == "applied" and animated["transition_id"], animated
                observed_settings = remote(env, identity, "status")["result"]
                assert observed_settings["theme"] == "paper", observed_settings
                assert observed_settings["playback"]["speed"] == 60, observed_settings
                assert not observed_settings["camera_animation"], observed_settings
                status = mcp.call("viewer_status", {"args": {}})["result"]["structuredContent"]
                assert len(status["result"]["journeys"]) == 1, status
                result = mcp.call("viewer_journey_reverse", {
                    "request_id": "reverse-once", "generation": status["generation"],
                    "expected_revision": status["revision"], "args": {"ref": journey}})
                assert not result["result"].get("isError", False), result
                duplicate = mcp.call("viewer_journey_reverse", {
                    "request_id": "reverse-once", "generation": status["generation"],
                    "expected_revision": status["revision"], "args": {"ref": journey}})
                assert result["result"]["structuredContent"] == duplicate["result"]["structuredContent"]
                observed = remote(env, identity, "journey", "inspect", "--journey",
                                  f'{journey["document_id"]}:{journey["item_id"]}')["result"]
                assert observed["stops"][0]["location"]["lat"] == 51.6, observed
                remote(env, identity, "journey", "show", "--journey",
                       f'{journey["document_id"]}:{journey["item_id"]}')
                # Plain CLI precondition failures are explicit and leave edits intact.
                failure = remote(env, identity, "camera", "set", "--zoom", "3",
                                 "--generation", status["generation"], "--expected-revision", "0", success=False)
                assert failure["error"]["code"] == "revision_conflict", failure
                # Requests may conflict at admission; accepted imports must never disappear.
                def load(path):
                    return subprocess.run([str(BINARY), "remote", "--session", identity,
                                           "load", path, "--json"], env=env, cwd=ROOT,
                                          capture_output=True, text=True, timeout=15)
                with ThreadPoolExecutor(max_workers=2) as pool:
                    for result in pool.map(load, ["fixtures/waypoints.gpx", "fixtures/country-ride.gpx"]):
                        response = json.loads(result.stdout)
                        assert response["state"] == "applied" or response["error"]["code"] == "revision_conflict", response
                # CLI save and MCP open share the viewer's checkpoint model.
                saved = remote(env, identity, "workspace", "save", "--name", "Agent tour")["result"]
                current = remote(env, identity, "status")
                opened = mcp.call("viewer_workspace_open", {
                    "args": {"path": saved["path"], "discard": True}, "wait": True,
                    "request_id": "open-saved-tour", "generation": current["generation"],
                    "expected_revision": current["revision"]})["result"]["structuredContent"]
                assert opened["state"] == "applied", opened
                assert opened["session_id"] == identity, opened
                assert opened["generation"] != current["generation"], opened
                assert opened["result"]["checkpoint"]["id"] == saved["id"], opened
                # Kill the process, reclaim its endpoint, and reuse its name.
                pid = int(tmux("display-message", "-p", "-t", "main", "#{pane_pid}").strip())
                os.kill(pid, 9)
                wait_for(lambda: len(sessions()) == 1)
                launch("main", extra=("--workspace", saved["path"]))
                wait_for(lambda: len(sessions()) == 2)
                result = mcp.call("viewer_status", {"args": {}})
                assert result["result"]["isError"], result
                assert remote(env, "main", "status")["session_id"] != identity
            finally:
                mcp.close()
            # Clean quit removes records, including the narrow ASCII viewer.
            for name in ("main", "second"):
                tmux("send-keys", "-t", name, "q")
            wait_for(lambda: not sessions())
        finally:
            tmux("kill-server", check=False)
    print("Validated real viewer CLI/MCP control, named sessions, isolation, retries, conflicts, crash cleanup and ASCII mode.")


if __name__ == "__main__":
    main()
