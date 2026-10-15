"""Exercise the release workflow in a real PTY, using the viewer's capture helpers."""
import shlex
from pathlib import Path

import interactions as terminal

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "reports/generated/release"


def key(value):
    terminal.tmux("send-keys", "-t", "poc:0.0", value)


def expect(value):
    return terminal.wait_for(lambda screen: value in screen)


def capture(name, width, height):
    (OUT / f"{name}.txt").write_text(terminal.capture())
    terminal.save_svg(OUT / f"{name}.svg", width, height)


def launch(scenario, delay="60ms"):
    command = shlex.join([
        "python3", str(ROOT / "scripts/terminal_runner.py"),
        str(ROOT / "bin/mapbox-release"), "--mode=test", f"--delay={delay}", f"--scenario={scenario}",
    ])
    terminal.tmux("new-session", "-d", "-s", "poc", "-x", "90", "-y", "32", command)
    expect("Edit suggested tag")


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    try:
        launch("delayed-workflow", "200ms")
        capture("ready", 90, 32)
        key("C-a")
        key("C-k")
        terminal.send("\x1b[200~v0.2.0\x1b[201~")
        key("Enter")
        expect("Would you like to push tag")
        capture("expanded-checklist", 90, 32)
        key("Enter")
        expect("Would you like to push tag")
        terminal.send("n")
        key("Enter")
        expect("Would you like to push tag")
        terminal.send("\x1b[200~y\x1b[201~")
        key("Enter")
        expect("Waiting for GitHub to start")
        expect("Did the offline viewer open")
        screen = terminal.capture()
        assert "Toolchain and gh authentication ready" not in screen, "completed output did not collapse"
        capture("completed-summaries", 90, 32)
        terminal.send("y")
        key("Enter")
        expect("Review notes:")
        terminal.send("e")
        expect("ctrl+s")
        capture("inline-notes-editor", 90, 32)
        terminal.send("Reviewed release notes. ")
        key("C-s")
        expect("Review notes:")
        terminal.send("y")
        key("Enter")
        expect("Have you published")
        terminal.tmux("resize-window", "-t", "poc:0", "-x", "90", "-y", "6")
        terminal.wait_for(lambda screen:
            "Have you published v0.2.0 on GitHub?" in screen and "[y/N]" in screen
            and any(line.strip().startswith(">") for line in screen.splitlines()))
        capture("short-confirmation", 90, 6)
        terminal.send("y")
        key("Enter")
        expect("Test complete")
        key("Enter")
        expect("TERMINAL_RESTORED=True EXIT=0")
        terminal.tmux("kill-session", "-t", "poc")
        launch("auth-error")
        key("Enter")
        expect("authentication failed")
        capture("failure", 90, 32)
        key("Enter")
        expect("Would you like to push tag")
        key("Escape")
        expect("TERMINAL_RESTORED=True EXIT=0")
        terminal.tmux("kill-session", "-t", "poc")
        print("Release acceptance: happy path (all steps, delayed workflow and inputs) and failure path (error, retry and cancellation) passed.")
    finally:
        terminal.tmux("kill-server", check=False)


if __name__ == "__main__":
    main()
