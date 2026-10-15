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
        expect("Push annotated tag")
        capture("expanded-checklist", 90, 32)
        terminal.send("no")
        key("Enter")
        expect("Push annotated tag")
        terminal.send("\x1b[200~yes\x1b[201~")
        key("Enter")
        expect("Waiting for GitHub to start")
        expect("confirm it started")
        key("PPage")
        key("PPage")
        key("PPage")
        expect("Toolchain and gh authentication ready")
        capture("scrolled-checklist", 90, 32)
        # Wheel down must move the same checklist, even while confirmation is focused.
        terminal.sgr(65, 20, 25)
        terminal.wait_for(lambda screen: "Toolchain and gh authentication ready" not in screen)
        key("NPage")
        key("NPage")
        key("NPage")
        expect("Draft attachments downloaded")
        terminal.send("yes")
        key("Enter")
        expect("Review notes:")
        terminal.send("e")
        expect("ctrl+s")
        capture("inline-notes-editor", 90, 32)
        terminal.send("Reviewed release notes. ")
        key("C-s")
        expect("Review notes:")
        terminal.send("yes")
        key("Enter")
        expect("Confirm publication")
        terminal.tmux("resize-window", "-t", "poc:0", "-x", "40", "-y", "22")
        terminal.wait_for(lambda screen:
            any(line.strip() == "v0.2.0 · example/mapbox-tui" for line in screen.splitlines())
            and "Type yes, then enter." in screen
            and any(line.strip().startswith(">") for line in screen.splitlines()))
        capture("narrow-confirmation", 40, 22)
        terminal.send("yes")
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
        expect("Push annotated tag")
        key("Escape")
        expect("TERMINAL_RESTORED=True EXIT=0")
        terminal.tmux("kill-session", "-t", "poc")
        print("Release acceptance: happy path (all steps, delayed workflow and inputs) and failure path (error, retry and cancellation) passed.")
    finally:
        terminal.tmux("kill-server", check=False)


if __name__ == "__main__":
    main()
