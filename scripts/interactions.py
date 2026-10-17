from pathlib import Path
import difflib
import html
import json
import os
import re
import shlex
import subprocess
import time
import sys
import tempfile
import tomllib

ROOT = Path(__file__).resolve().parents[1]
SOCKET = f"mapbox-tui-{os.getpid()}"
ANSI = re.compile(r"\x1b\[([0-9;]*)m")
HYPERLINK = re.compile(r"\x1b\]8;[^\x07\x1b]*(?:\x07|\x1b\\)")


def tmux(*args, check=True):
    return subprocess.run(["tmux", "-L", SOCKET, "-f", "/dev/null", *args], capture_output=True, text=True, check=check).stdout


def capture(colors=False):
    text = tmux("capture-pane", "-p", *( ["-e"] if colors else []), "-t", "poc:0.0")
    if colors:
        return text
    width = int(tmux("display-message", "-p", "-t", "poc:0.0", "#{pane_width}"))
    lines = []
    for line in text.splitlines():
        expanded, column = [], 0
        for char in line:
            if char == "\t":
                # A terminal tab stops at the rightmost cell, even before the next tab stop.
                spaces = max(0, min(8 - column % 8, width - 1 - column))
                expanded.append(" " * spaces)
                column += spaces
            else:
                expanded.append(char)
                column += 1
        lines.append("".join(expanded))
    return "\n".join(lines) + "\n"


def wait_for(predicate, timeout=10):
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        screen = capture()
        if predicate(screen):
            return screen
        time.sleep(.03)
    raise AssertionError(f"Interaction timed out:\n{capture()}")


def settled():
    previous = capture()
    unchanged = time.monotonic()
    until = time.monotonic() + 5
    while time.monotonic() < until:
        time.sleep(.05)
        current = capture()
        if current != previous:
            previous, unchanged = current, time.monotonic()
        elif time.monotonic() - unchanged >= .3:
            return current
    raise AssertionError(f"Camera did not settle:\n{capture()}")


def send(text):
    tmux("send-keys", "-t", "poc:0.0", "-l", text)


def sgr(button, x, y, release=False):
    send(f"\x1b[<{button};{x};{y}{'m' if release else 'M'}")


def xterm_color(n):
    if n < 16:
        return ["#000000", "#800000", "#008000", "#808000", "#000080", "#800080", "#008080", "#c0c0c0", "#808080", "#ff0000", "#00ff00", "#ffff00", "#0000ff", "#ff00ff", "#00ffff", "#ffffff"][n]
    if n >= 232:
        return "#" + f"{8 + (n - 232) * 10:02x}" * 3
    n -= 16
    levels = [0, 95, 135, 175, 215, 255]
    return "#" + "".join(f"{levels[v]:02x}" for v in (n // 36, n // 6 % 6, n % 6))


def styled_runs(screen, width, background="#1e1e2e", foreground="#cdd6f4"):
    # OSC 8 metadata occupies no terminal cells and must not enter SVG text.
    screen = HYPERLINK.sub("", screen)
    fg = foreground
    bg = background
    bold = False
    for y, line in enumerate(screen.splitlines()):
        x, position = 0, 0
        for match in [*ANSI.finditer(line), None]:
            end = match.start() if match else len(line)
            chunk = line[position:end]
            if chunk:
                expanded, column = [], x
                for char in chunk:
                    if char == "\t":
                        spaces = max(0, min(8 - column % 8, width - 1 - column))
                        expanded.append(" " * spaces)
                        column += spaces
                    else:
                        expanded.append(char)
                        column += 1
                chunk = "".join(expanded)
                yield x, y, chunk, fg, bg, bold
                x += len(chunk)
            if match:
                nums = [int(n or 0) for n in match.group(1).split(';')]
                i = 0
                while i < len(nums):
                    n = nums[i]
                    if n in (0, 39):
                        fg = foreground
                        if n == 0:
                            bg = background
                            bold = False
                    elif n == 1:
                        bold = True
                    elif n == 22:
                        bold = False
                    elif n == 49:
                        bg = background
                    elif 30 <= n <= 37:
                        fg = xterm_color(n - 30)
                    elif 40 <= n <= 47:
                        bg = xterm_color(n - 40)
                    elif n == 48 and i + 2 < len(nums) and nums[i+1] == 5:
                        bg = xterm_color(nums[i+2]); i += 2
                    elif n == 48 and i + 4 < len(nums) and nums[i+1] == 2:
                        bg = "#" + "".join(f"{v:02x}" for v in nums[i+2:i+5]); i += 4
                    elif n == 38 and i + 2 < len(nums) and nums[i+1] == 5:
                        fg = xterm_color(nums[i+2]); i += 2
                    elif n == 38 and i + 4 < len(nums) and nums[i+1] == 2:
                        fg = "#" + "".join(f"{v:02x}" for v in nums[i+2:i+5]); i += 4
                    i += 1
                position = match.end()


def visible_cells(screen, width, background="#1e1e2e", foreground="#cdd6f4"):
    # A blank cell's foreground and weight do not affect its appearance. tmux can
    # retain these attributes after a compositor redraw without changing the map.
    return {
        (x + offset, y): (char, fg if char != " " else None, bg, bold if char != " " else False)
        for x, y, chunk, fg, bg, bold in styled_runs(screen, width, background, foreground)
        for offset, char in enumerate(chunk)
    }


def save_svg(path, width, height, background="#1e1e2e", foreground="#cdd6f4"):
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width*9}" height="{height*18}" viewBox="0 0 {width*9} {height*18}">', f'<rect width="100%" height="100%" fill="{background}"/>', '<g font-family="DejaVu Sans Mono,monospace" font-size="15" xml:space="preserve">']
    for x, y, chunk, fg, bg, bold in styled_runs(capture(colors=True), width, background, foreground):
        if bg != background:
            out.append(f'<rect x="{x*9}" y="{y*18}" width="{len(chunk)*9}" height="18" fill="{bg}"/>')
        weight = ' font-weight="bold"' if bold else ''
        out.append(f'<text x="{x*9}" y="{y*18+14}" fill="{fg}"{weight}>{html.escape(chunk)}</text>')
    out.extend(['</g>', '</svg>'])
    path.write_text('\n'.join(out) + '\n')
    subprocess.run(["rsvg-convert", "-o", str(path.with_suffix('.png')), str(path)], check=True)


def active_modal(screen, title):
    return re.search(r"[╭+][─-] .*?" + re.escape(title) + r" [─-]+[╮+]", screen) is not None


def context_text():
    """Read scrollable context, then return focus to the unchanged map."""
    if "ctrl+b hide" not in capture():
        tmux("send-keys", "-t", "poc:0.0", "C-b")
    tmux("send-keys", "-t", "poc:0.0", "Home")
    wait_for(lambda screen: "ctrl+b hide" in screen)
    pages = []
    previous = None
    unchanged = 0
    tiny = len(capture().splitlines()) <= 18
    for _ in range(60 if tiny else 20):
        time.sleep(.05)
        page = capture()
        unchanged = unchanged + 1 if page == previous else 0
        # A one-line viewport can show consecutive blank lines before the end.
        if unchanged >= (3 if tiny else 1):
            break
        pages.append(page)
        previous = page
        tmux("send-keys", "-t", "poc:0.0", "PageDown")
    tmux("send-keys", "-t", "poc:0.0", "C-b")
    wait_for(lambda screen: "ctrl+b sidebar" in screen)
    return "\n".join(pages)


def wait_context(predicate, timeout=10):
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        text = context_text()
        if predicate(text):
            return text
    raise AssertionError(f"Context assertion timed out:\n{text}")


def scenario(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx")])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "Paused 1x" in s and "ctrl+b sidebar" in s)
        assert "Elevation profile" in capture(), "recorded elevation should appear automatically"
        settled()
        send("f")
        initial = settled()
        folder = ROOT / "reports/generated"
        folder.mkdir(exist_ok=True, parents=True)
        save_svg(folder / f"{binary.name}-{width}x{height}.svg", width, height)
        tmux("send-keys", "-t", "poc:0.0", "C-b", "Home")
        wait_for(lambda s: "ctrl+b hide" in s and "Selected position" in s)
        save_svg(folder / f"{binary.name}-context-{width}x{height}.svg", width, height)
        tmux("send-keys", "-t", "poc:0.0", "C-b")
        wait_for(lambda s: "ctrl+b sidebar" in s)
        restored = settled()
        assert restored == initial, "context drawer changed the map:\n" + "\n".join(difflib.unified_diff(initial.splitlines(), restored.splitlines()))
        assert "No basemap" in initial and "token missing" not in initial, "token-free GPX viewing did not use a quiet empty canvas"
        tmux("send-keys", "-t", "poc:0.0", "C-b")
        before = wait_for(lambda s: "ctrl+b hide" in s)
        tmux("send-keys", "-t", "poc:0.0", "Right")
        wait_for(lambda s: s != before and "ctrl+b hide" in s)
        before = settled()
        sgr(0, 20, 6); sgr(32, 25, 6); sgr(0, 25, 6, release=True)
        wait_for(lambda s: s != before and "ctrl+b hide" in s)
        before = settled()
        sgr(64, 20, 6)
        wait_for(lambda s: s != before and "ctrl+b hide" in s)
        tmux("send-keys", "-t", "poc:0.0", "C-b")
        wait_for(lambda s: "ctrl+b sidebar" in s)
        send("f")
        settled()
        zoom = re.search(r"Zoom ([\d.]+)", context_text()).group(1)
        send("]")
        wait_context(lambda s: "Point 2 / 10" in s)
        send(".")
        wait_context(lambda s: "Bearing 10" in s)
        send("n")
        wait_context(lambda s: "Bearing 0°" in s)
        sgr(2, 30, 15); sgr(34, 35, 15); sgr(2, 35, 15, release=True)
        wait_context(lambda s: "Bearing 10" in s)
        send("n")
        wait_context(lambda s: "Bearing 0°" in s)
        sgr(64, 30, 15)
        single = wait_context(lambda s: re.search(r"Zoom ([\d.]+)", s).group(1) != zoom)
        single_zoom = float(re.search(r"Zoom ([\d.]+)", single).group(1))
        assert abs(single_zoom - float(zoom) - .1) < .011, "wheel step differs from 0.1"
        send("+")
        keyboard = wait_context(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > single_zoom)
        keyboard_zoom = float(re.search(r"Zoom ([\d.]+)", keyboard).group(1))
        assert abs(keyboard_zoom - single_zoom - .1) < .011, "keyboard step differs from 0.1"
        send("\x1b[<64;30;15M" * 8)
        burst = wait_context(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > keyboard_zoom)
        burst_zoom = float(re.search(r"Zoom ([\d.]+)", burst).group(1))
        assert burst_zoom - keyboard_zoom < .4, "rapid wheel burst jumped multiple levels"
        before_modified_zoom = float(re.search(r"Zoom ([\d.]+)", context_text()).group(1))
        sgr(80, 30, 15)
        modified = wait_context(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > before_modified_zoom)
        modified_zoom = float(re.search(r"Zoom ([\d.]+)", modified).group(1))
        assert abs(modified_zoom - before_modified_zoom - .2) < .011, "Ctrl+wheel did not double the zoom step"
        before_horizontal = context_text()
        send("\x1b[<66;30;15M\x1b[<67;30;15M")
        send("v")
        wait_for(lambda s: "Map credits" in s)
        send("v")
        assert "Map credits" in settled(), "repeated shortcut closed credits"
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Map credits" not in s)
        horizontal = context_text()
        assert re.search(r"Zoom ([\d.]+)", horizontal).group(1) == re.search(r"Zoom ([\d.]+)", before_horizontal).group(1), "horizontal wheel zoomed the map"
        send("f")
        wait_context(lambda s: f"Zoom {zoom}" in s)
        before = settled()
        sgr(0, 30, 15); sgr(32, 35, 17); sgr(0, 35, 17, release=True)
        wait_for(lambda s: s != before)
        send("f")
        restored = settled()
        assert restored == before, "fit did not restore the view:\n" + "\n".join(difflib.unified_diff(before.splitlines(), restored.splitlines()))
        # Click the displayed start marker rather than a guessed coordinate.
        for y, row in enumerate(before.splitlines()):
            if 2 <= y < height - 4 and 'S' in row[:width-30]:
                x = row.index('S')
                sgr(0, x+1, y+1); sgr(0, x+1, y+1, release=True)
                wait_context(lambda s: any(label in s for label in ("Point 1 / 10", "Point 7 / 10", "Point 1 / 2")))
                break
        else:
            raise AssertionError("start marker not visible")
        send("a")
        wait_for(lambda s: not any(0x2800 <= ord(c) <= 0x28ff for c in s))
        send("v")
        wait_for(lambda s: "Map credits" in s and "No basemap" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        tmux("resize-window", "-t", "poc:0", "-x", "70", "-y", "24")
        wait_for(lambda s: "ctrl+b sidebar" in s and "/ search" in s and "? help" in s)
        tmux("resize-window", "-t", "poc:0", "-x", "25", "-y", "10")
        wait_for(lambda s: "Enlarge terminal" in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: keyboard, SGR wheel/pan/click/right-drag, north reset, ASCII, narrow/tiny resize, clean exit passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def live_capture(binary, width, height):
    command = shlex.join(["env", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx"), "--center-lon", "-0.1065", "--center-lat", "51.505", "--zoom", "13", "--point-index", "3"])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "ctrl+b sidebar" in s)
        screen = wait_context(lambda s: "Route details" in s and "© Mapbox" in s and "Loading basemap" not in s and re.search(r"\b[1-9]\d* / 0 / 0", s), timeout=25)
        folder = ROOT / "reports/generated"
        folder.mkdir(exist_ok=True, parents=True)
        save_svg(folder / f"{binary.name}-mapbox-{width}x{height}.svg", width, height)
        stats = re.search(r"(\d+ / \d+ / \d+)", screen).group(1)
        send("]")
        screen = wait_context(lambda s: "Point 5 / 10" in s)
        assert re.search(r"(\d+ / \d+ / \d+)", screen).group(1) == stats, "selection refetched tiles"
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} Mapbox {width}×{height}: live basemap, route inspection, no requests on selection, terminal restoration passed ({stats})")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def tiny_start(binary):
    command=shlex.join(["env","MAPBOX_ACCESS_TOKEN=","TERM=xterm-256color","python3",str(ROOT / "scripts/terminal_runner.py"),str(binary),"open",str(ROOT / "fixtures/example.gpx")])
    tmux("new-session","-d","-s","poc","-x","25","-y","10",command)
    try:
        wait_for(lambda s:"Enlarge terminal" in s)
        tmux("resize-window","-t","poc:0","-x","120","-y","40")
        wait_for(lambda s:"Paused 1x" in s)
        send("f")
        send("q")
        wait_for(lambda s:"TERMINAL_RESTORED=True EXIT=0" in s)
        print(f"{binary.name}: tiny-terminal startup and enlargement passed")
    finally:
        tmux("kill-session","-t","poc",check=False)


def profile_playback(binary, width, height):
    command=shlex.join(["env","MAPBOX_ACCESS_TOKEN=","TERM=xterm-256color","COLORTERM=truecolor","python3",str(ROOT / "scripts/terminal_runner.py"),str(binary),str(ROOT / "fixtures/example.gpx")])
    tmux("new-session","-d","-s","poc","-x",str(width),"-y",str(height),command)
    try:
        wait_for(lambda s: "ctrl+b sidebar" in s)
        wait_for(lambda s:"Elevation profile" in s and "Loading basemap" not in s)
        wait_for(lambda s:"Elevation profile" in s and "@ 0.00 km" in s)
        send("e")
        wait_for(lambda s:"Elevation profile" not in s)
        send("e")
        wait_for(lambda s:"Elevation profile" in s and "@ 0.00 km" in s)
        send("ssss")
        wait_for(lambda s:"Paused 120x" in s)
        send(" ")
        wait_context(lambda s:"Playing 120x" in s and "Free camera" in s and any(float(value)>.1 for value in re.findall(r"@ ([\d.]+) km",s)))
        send("c")
        wait_context(lambda s:"Playing 120x" in s and "Following position" in s)
        send(" ")
        wait_for(lambda s:"Paused 120x" in s)
        paused=settled()
        assert "Between " in context_text() and "~" in paused, "playback did not interpolate recorded samples"
        time.sleep(.12)
        assert capture()==paused, "pause kept changing ride position"
        folder=ROOT / "reports/generated"
        folder.mkdir(exist_ok=True,parents=True)
        save_svg(folder / f"{binary.name}-profile-{width}x{height}.svg",width,height)
        send("S")
        wait_for(lambda s:"Paused 60x" in s)
        tmux("send-keys","-t","poc:0.0","End")
        wait_context(lambda s:"Point 10 / 10" in s and "Finished 60x" in s)
        send(" ")
        wait_context(lambda s:"Playing 60x" in s and "Between 1–2" in s)
        send("h")
        wait_context(lambda s:"Playing 60x" in s and "Free camera" in s)
        send(" ")
        wait_for(lambda s:"Paused 60x" in s)
        tmux("resize-window","-t","poc:0","-x","70","-y","24")
        wait_for(lambda s:"Elevation profile" in s and "ctrl+b sidebar" in s)
        tmux("resize-window","-t","poc:0","-x","70","-y","18")
        wait_for(lambda s:"Elevation profile" in s and "Paused 60x" in s)
        send("a")
        wait_for(lambda s:not any(0x2800<=ord(c)<=0x28ff for c in s))
        send("e")
        wait_for(lambda s:"Elevation profile" not in s)
        send("q")
        wait_for(lambda s:"TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+","",s))
        print(f"{binary.name} {width}×{height}: elevation toggle, interpolated playback, pause, speeds, follow, end/restart, compact/ASCII profile passed")
    finally:
        tmux("kill-session","-t","poc",check=False)


def live_playback(binary):
    command=shlex.join(["env","TERM=xterm-256color","COLORTERM=truecolor","python3",str(ROOT / "scripts/terminal_runner.py"),str(binary),"open",str(ROOT / "fixtures/example.gpx"),"--zoom","14","--point-index","3"])
    tmux("new-session","-d","-s","poc","-x","120","-y","40",command)
    try:
        wait_for(lambda s: "ctrl+b sidebar" in s)
        wait_context(lambda s:"Route details" in s and "Loading basemap" not in s and re.search(r"\b[1-9]\d* / 0 / 0",s),timeout=25)
        before=wait_context(lambda s:"Elevation profile" in s)
        base_hits=int(re.search(r"(\d+) / (\d+) / (\d+)",before).group(2))
        send("ssss ")
        wait_context(lambda s:"Playing 120x" in s and "Free camera" in s)
        send("c")
        def moving(s):
            stats=re.search(r"(\d+) / (\d+) / (\d+)",s)
            return "Playing 120x" in s and stats and int(stats.group(2))>base_hits and int(stats.group(3))==0
        screen=wait_context(moving,timeout=10)
        assert screen.splitlines()[-1].startswith("© Mapbox"), "attribution was not anchored lower-left"
        send(" ")
        wait_for(lambda s:"Paused 120x" in s)
        folder=ROOT / "reports/generated"
        folder.mkdir(exist_ok=True,parents=True)
        save_svg(folder / f"{binary.name}-mapbox-profile-120x40.svg",120,40)
        send("q")
        wait_for(lambda s:"TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+","",s))
        print(f"{binary.name}: live playback followed the route, refreshed cached tiles while moving, and kept lower-left attribution")
    finally:
        tmux("kill-session","-t","poc",check=False)


def dense_profile(binary):
    path=ROOT / "fixtures/dense-100000.gpx"
    if not path.exists():
        return
    command=shlex.join(["env","MAPBOX_ACCESS_TOKEN=","TERM=xterm-256color","python3",str(ROOT / "scripts/terminal_runner.py"),str(binary),"open",str(path)])
    tmux("new-session","-d","-s","poc","-x","120","-y","40",command)
    try:
        wait_for(lambda s: "ctrl+b sidebar" in s)
        wait_context(lambda s:"100000 points" in s)
        wait_for(lambda s:"Elevation profile" in s)
        send("ss ")
        wait_context(lambda s:"Playing 20x" in s and re.search(r"Playing 20x\s+00:0[5-9]",s))
        send(" ")
        wait_for(lambda s:"Paused 20x" in s)
        send("q")
        wait_for(lambda s:"TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+","",s))
        print(f"{binary.name}: 100k-point profile and wall-clock playback passed")
    finally:
        tmux("kill-session","-t","poc",check=False)


def dialogs(binary):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary)])
    tmux("new-session", "-d", "-s", "poc", "-x", "120", "-y", "40", command)
    try:
        wait_for(lambda s: "Free roam" in s and "ctrl+b sidebar" in s)
        send("\x0f")
        wait_for(lambda s: "Open track file" in s)

        def select_file(name, activate="Enter"):
            tmux("send-keys", "-t", "poc:0.0", "g")
            for _ in range(20):
                current = capture()
                if re.search(r"›\s+" + re.escape(name), current):
                    tmux("send-keys", "-t", "poc:0.0", activate)
                    return
                send("j")
                wait_for(lambda s: s != current)
            raise AssertionError(f"File picker did not reach {name}")

        select_file("fixtures", "Right")
        wait_for(lambda s: "example.gpx" in s and "/fixtures" in s)
        folder = ROOT / "reports/generated"
        save_svg(folder / f"{binary.name}-filepicker-120x40.svg", 120, 40)
        select_file("example.gpx")
        wait_for(lambda s: "Paused 1x" in s and "Open track file" not in s)
        before = settled()
        send("?")
        wait_for(lambda s: "Keyboard shortcuts" in s and "playback continues" in s)
        save_svg(folder / f"{binary.name}-help-120x40.svg", 120, 40)
        send("h")
        sgr(64, 30, 15)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Keyboard shortcuts" not in s)
        after = [line.rstrip() for line in settled().splitlines()]
        expected = [line.rstrip() for line in before.splitlines()]
        assert after == expected, "help input changed the map underneath:\n" + "\n".join(difflib.unified_diff(expected, after))
        send("\x0b")
        wait_for(lambda s: active_modal(s, "Commands") and "Type a command" in s)
        send("speed")
        wait_for(lambda s: "Playback speed" in s and "Open track file" not in s)
        save_svg(folder / f"{binary.name}-commands-120x40.svg", 120, 40)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Playback speed multiplier" in s)
        tmux("send-keys", "-t", "poc:0.0", "Up")
        wait_for(lambda s: "› 120x" in s)
        tmux("send-keys", "-t", "poc:0.0", "Down")
        wait_for(lambda s: "› 1x" in s)
        save_svg(folder / f"{binary.name}-speed-120x40.svg", 120, 40)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: active_modal(s, "Commands") and "› speed" in s and "Playback speed multiplier" not in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Playback speed multiplier" in s and "esc back" in s)
        send("jj")
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Paused 20x" in s and "Playback speed multiplier" not in s)
        send("\x0f")
        wait_for(lambda s: "Open track file" in s and "example.gpx" in s)
        select_file("invalid.gpx")
        wait_for(lambda s: "Could not open track file" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Riverside exploration" in s and "Open track file" not in s)
        send("\x0f")
        wait_for(lambda s: "Open track file" in s and "waypoints.gpx" in s)
        select_file("waypoints.gpx")
        wait_for(lambda s: "Only a waypoint" in s and "Open track file" not in s)
        send("\x0b")
        wait_for(lambda s: active_modal(s, "Commands") and "Type a command" in s)
        send("free roam")
        wait_for(lambda s: "Free roam (clear selection)" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Free roam" in s and "Commands" not in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name}: startup/file picker, failed-load recovery, modal input isolation, command filtering, speed picker, file additions and free roaming passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def free_roam(binary, live=False):
    env = ["env", "TERM=xterm-256color", "COLORTERM=truecolor"]
    args = ["--center-lon", "-0.1065", "--center-lat", "51.505", "--zoom", "13"] if live else []
    if not live:
        env.append("MAPBOX_ACCESS_TOKEN=")
    command = shlex.join([*env, "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), *args])
    tmux("new-session", "-d", "-s", "poc", "-x", "120", "-y", "40", command)
    try:
        footer = "© Mapbox" if live else "Mapbox not connected"
        initial = wait_for(lambda s: "Free roam" in s and
                           "Loading basemap" not in s and footer in s, timeout=25)
        assert "Route details" not in initial and "Elevation profile" not in initial and "Paused" not in initial
        if live:
            assert "© Mapbox" in initial and re.search(r"\b[1-9]\d* / 0 / 0", context_text()), initial
        else:
            assert "No basemap" in initial and "Mapbox not connected" in initial, f"empty-canvas startup did not show connection status:\n{initial}"
        before = context_text()
        send("h")
        wait_context(lambda s: (match := re.search(r"Lon (-?[\d.]+)", s)) is not None and
                 match.group(1) != re.search(r"Lon (-?[\d.]+)", before).group(1))
        sgr(0, 30, 15); sgr(32, 35, 16); sgr(0, 35, 16, release=True)
        sgr(64, 30, 15)
        wait_for(lambda s: (match := re.search(r"Zoom ([\d.]+)", s)) is not None and
                 match.group(1) != re.search(r"Zoom ([\d.]+)", before).group(1))
        current = settled()
        zoom = float(re.search(r"Zoom ([\d.]+)", current).group(1))
        sgr(80, 30, 15)
        modified = wait_for(lambda s: (match := re.search(r"Zoom ([\d.]+)", s)) is not None and float(match.group(1)) > zoom)
        assert abs(float(re.search(r"Zoom ([\d.]+)", modified).group(1)) - zoom - .2) < .011, "Ctrl+wheel did not double free-roam zoom"
        sgr(0, 30, 15); sgr(0, 30, 15, release=True)
        wait_for(lambda s: "Selected position" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Selected position" in s and "↑/↓ scroll" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "↑/↓ scroll" not in s)
        send("?")
        wait_for(lambda s: "fit all visible" in s and "Keyboard shortcuts" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Keyboard shortcuts" not in s)
        folder = ROOT / "reports/generated"
        folder.mkdir(exist_ok=True, parents=True)
        settled()
        save_svg(folder / f"{binary.name}-free-roam{'-mapbox' if live else ''}-120x40.svg", 120, 40)
        tmux("resize-window", "-t", "poc:0", "-x", "70", "-y", "24")
        wait_for(lambda s: "ctrl+b sidebar" in s and "/ search" in s and "? help" in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name}: {'live Mapbox' if live else 'offline'} free roam, keyboard/mouse navigation, coordinate inspection, help, compact resize and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def theme_switching(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx"), "--theme", "catppuccin"])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "ctrl+b sidebar" in s)
        wait_for(lambda s: "Elevation profile" in s and "Paused 1x" in s)
        baseline = settled()
        original_colors = capture(colors=True)
        palettes = [tomllib.loads(path.read_text()) for path in sorted((ROOT / "go/themes").glob("*.toml"))]
        names = tuple(palette["name"] for palette in palettes)
        default = next(i for i, palette in enumerate(palettes) if palette["id"] == "catppuccin")
        synthwave = next(i for i, palette in enumerate(palettes) if palette["id"] == "synthwave")
        preview = (default - 1) % len(palettes)
        previous = (preview - 1) % len(palettes)

        def open_picker():
            send("\x0b")
            wait_for(lambda s: active_modal(s, "Commands"))
            send("theme")
            wait_for(lambda s: "Colour theme" in s)
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            return wait_for(lambda s: "Colour theme" in s and "(current)" in s and "esc back" in s)

        def select_theme(index):
            screen = open_picker()
            selected = next(i for i, name in enumerate(names) if f"> {name}" in screen)
            steps = (index - selected) % len(names)
            if steps:
                tmux("send-keys", "-t", "poc:0.0", *(["Down"] * steps))
            wait_for(lambda s: f"> {names[index]}" in s)
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: "Colour theme" not in s and "Commands" not in s and f"Theme: {names[index]}" in s)

        screen = open_picker()
        assert f"> {names[default]} (current)" in screen, "theme list lost the selected current palette"
        assert "Commands › Colour theme" in screen and screen.count("╭") <= 2, "theme picker lost breadcrumbs or added parent frames"
        save_svg(ROOT / "reports/generated" / f"{binary.name}-theme-picker-{width}x{height}.svg", width, height)
        picker_colors = capture(colors=True)
        baseline_cells = visible_cells(original_colors, width)
        picker_cells = visible_cells(picker_colors, width)
        assert all(picker_cells[position] == cell for position, cell in baseline_cells.items() if position[0] < 8), "theme picker dimmed the visible map"
        tmux("send-keys", "-t", "poc:0.0", "Up")
        wait_for(lambda s: f"> {names[preview]}" in s and f"Theme: {names[preview]}" in s and capture(colors=True) != picker_colors)
        tmux("send-keys", "-t", "poc:0.0", "Up")
        wait_for(lambda s: f"> {names[previous]}" in s)
        tmux("send-keys", "-t", "poc:0.0", "Down")
        wait_for(lambda s: f"> {names[preview]}" in s)
        palette = palettes[preview]
        save_svg(ROOT / "reports/generated" / f"{binary.name}-theme-preview-{width}x{height}.svg", width, height, palette["background"], palette["text"])
        before = settled()
        colors = capture(colors=True)
        send("\x14")
        assert settled() == before and capture(colors=True) == colors, "removed theme shortcut still changed the picker"
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: active_modal(s, "Commands") and "› theme" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Commands" not in s)
        assert settled() == baseline and visible_cells(capture(colors=True), width) == baseline_cells, "cancelled theme selection applied a palette"
        for index in [i for i in range(len(palettes)) if i != default] + [default]:
            palette = palettes[index]
            select_theme(index)
            save_svg(ROOT / "reports/generated" / f"{binary.name}-theme-{palette['id']}-{width}x{height}.svg", width, height, palette["background"], palette["text"])
            if index == synthwave:
                open_picker()
                save_svg(ROOT / "reports/generated" / f"{binary.name}-theme-synthwave-picker-{width}x{height}.svg", width, height, palette["background"], palette["text"])
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: active_modal(s, "Commands") and "› theme" in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: "Commands" not in s)
        assert settled() == baseline, "theme selection changed geometry, selection or playback"
        select_theme((default + 1) % len(palettes))
        wait_for(lambda s: capture(colors=True) != original_colors)
        changed = settled()
        footer_rows = 2 if width >= 110 else 3
        assert changed.splitlines()[:-footer_rows] == baseline.splitlines()[:-footer_rows], "palette change affected map text or layout"
        send("\x10:")
        assert settled() == changed, "removed Commands shortcut or deferred fallback opened a menu"
        for key, label in (("?", "Keyboard shortcuts"), ("v", "Map credits"), ("p", "Playback speed multiplier"), ("\x0c", "Loaded files"), ("\x0f", "Open track file"), ("\x0b", "Commands")):
            send(key)
            wait_for(lambda s: label in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: label not in s)
        send("a ")
        wait_for(lambda s: "Playing 1x" in s and not any(0x2800 <= ord(c) <= 0x28ff for c in s))
        for index, palette in enumerate(palettes):
            select_theme(index)
            wait_for(lambda s: "Playing 1x" in s and not any(0x2800 <= ord(c) <= 0x28ff for c in s))
            save_svg(ROOT / "reports/generated" / f"{binary.name}-theme-{palette['id']}-ascii-{width}x{height}.svg", width, height, palette["background"], palette["text"])
        send(" q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: live theme preview, {len(palettes)} palettes, confirm/cancel restoration, removed shortcut, recoloured dialogs, unchanged geometry, ASCII playback and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def command_palette_access(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx")])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "Paused 1x" in s and "Loading track file" not in s)
        send("f")
        settled()

        def round_trip(view):
            before = settled()
            send("\x0b")
            wait_for(lambda s: active_modal(s, "Commands") and "Type a command" in s)
            send("keyboard")
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: active_modal(s, "Keyboard shortcuts") and "esc back" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: active_modal(s, "Commands") and "› keyboard" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: not active_modal(s, "Commands"))
            after = settled()
            difference = "\n".join(difflib.unified_diff(before.splitlines(), after.splitlines(), fromfile="before", tofile="after"))
            assert after == before, f"command palette did not restore {view}:\n{difference}"

        for ascii_mode in (False, True):
            if ascii_mode:
                send("a")
                wait_for(lambda s: not any(0x2800 <= ord(c) <= 0x28ff for c in s))
            send("\x02")
            wait_for(lambda s: "ctrl+b hide" in s)
            round_trip("Context")
            send("\x0c")
            wait_for(lambda s: "Loaded files" in s)
            tmux("send-keys", "-t", "poc:0.0", "End")
            round_trip("Files selection")
            send("\x02")
            wait_for(lambda s: "Loaded files" not in s)
            round_trip("collapsed Files")
            send("\x0a")
            wait_for(lambda s: "A Start" in s and "Context · Files · Journey" in s)
            round_trip("Journey")
            tmux("send-keys", "-t", "poc:0.0", "Home", "Enter")
            wait_for(lambda s: "Place or latitude" in s)
            send("51.31, -2.58")
            round_trip("journey location input")
            editor_before = settled()
            send("\x0b")
            wait_for(lambda s: active_modal(s, "Commands") and "Type a command" in s)
            send("Journey Planner")
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: "> A Start" in s)
            tmux("send-keys", "-t", "poc:0.0", "Home", "Down", "Down", "Enter")
            wait_for(lambda s: "B Destination" in s and "Place or latitude" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "> B Destination" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: active_modal(s, "Commands") and "› Journey Planner" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "A Start" in s and "51.31, -2.58" in s and not active_modal(s, "Commands"))
            assert settled() == editor_before, "nested planner changed the original location editor's target or input"
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "> A Start" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "Context · Files · Journey" not in s)
            send("/51.32, -2.59")
            wait_for(lambda s: "Quick search" in s)
            round_trip("quick-search input")
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "Quick search" not in s)
            send("\x0f")
            wait_for(lambda s: "Open track file" in s and "example.gpx" in s)
            round_trip("file picker")
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "Open track file" not in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: command palette from Context, Files, collapsed Files, Journey, location input, quick search and file picker; nested planner ownership, command execution, return state, ASCII and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def workspace_controls(binary):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx"), str(ROOT / "fixtures/waypoints.gpx")])
    tmux("new-session", "-d", "-s", "poc", "-x", "120", "-y", "40", command)
    try:
        wait_for(lambda s: "Only a waypoint" in s and "ctrl+b sidebar" in s and "Loading track file" not in s)
        send("\x0c")
        wait_for(lambda s: "Loaded files" in s and "example.gpx" in s and "waypoints.gpx" in s)
        save_svg(ROOT / "reports/generated" / f"{binary.name}-workspace-120x40.svg", 120, 40)
        send(" ")
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Free roam" in s and "Loaded files" not in s)
        send("\x0c")
        wait_for(lambda s: "Loaded files" in s)
        tmux("send-keys", "-t", "poc:0.0", "End")
        send("D")
        wait_for(lambda s: "Loaded files" in s and "example.gpx" in s and "waypoints.gpx" not in s)
        tmux("send-keys", "-t", "poc:0.0", "Home", "Enter")
        wait_for(lambda s: "Loaded files" not in s and "Riverside exploration" in s and "Paused 1x" in s)
        send("f")
        settled()
        tmux("resize-window", "-t", "poc:0", "-x", "70", "-y", "24")
        send("\x0c")
        wait_for(lambda s: "Loaded files" in s)
        tmux("send-keys", "-t", "poc:0.0", "End")
        wait_for(lambda s: "Loaded files" in s and "Optional spur" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Loaded files" not in s and "Optional spur" in s)
        send("\x0b")
        wait_for(lambda s: active_modal(s, "Commands"))
        send("clear workspace")
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Free roam" in s and "Commands" not in s)
        send("\x0c")
        wait_for(lambda s: "No files loaded" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Loaded files" not in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name}: multi-file startup, loaded filenames, visibility, file removal, item selection, compact dialog and clear workspace passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def workspace_profiles(binary, width, height):
    folder = ROOT / "reports/generated"
    folder.mkdir(exist_ok=True, parents=True)
    with tempfile.TemporaryDirectory(dir=folder) as temporary:
        paths = []
        for name, latitude, step, elevations in (("Short loop", 51.501, .004, (40, 100, 60)), ("Long ride", 51.508, .008, (25, 70, 40))):
            points = "".join(f'<trkpt lat="{latitude}" lon="{-0.124 + i*step}"><ele>{elevation}</ele><time>2026-01-01T09:{i*10:02d}:00Z</time></trkpt>' for i, elevation in enumerate(elevations))
            path = Path(temporary) / f"{name}.gpx"
            path.write_text(f"<gpx><trk><name>{name}</name><trkseg>{points}</trkseg></trk></gpx>")
            paths.append(str(path))
        command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), *paths])
        tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
        try:
            wait_for(lambda s: "ctrl+b sidebar" in s)
            initial = wait_for(lambda s: "Elevation profiles" in s and "Short loop" in s and "Long ride" in s and "Loading track file" not in s)
            start_distance = float(re.search(r"@ ([\d.]+) km", initial).group(1))
            total_distance = float(re.search(r"([\d.]+) km total", initial).group(1))
            assert 0 < start_distance < total_distance and abs(start_distance / total_distance - 1/3) < .01, "second activity did not start after the first on the summed distance axis"
            legend = next(line for line in initial.splitlines() if "1:1 Short loop" in line and "2:1 Long ride" in line)
            assert legend.index("Short loop") < legend.index("Long ride"), "legend changed load order to put the selected activity first"
            send("f")
            settled()
            save_svg(folder / f"{binary.name}-two-profiles-{width}x{height}.svg", width, height)
            send("ssss ")
            wait_for(lambda s: "Long ride" in s and "Playing 120x" in s and any(float(value) > start_distance + .01 for value in re.findall(r"@ ([\d.]+) km", s)))
            send(" ")
            wait_for(lambda s: "Paused 120x" in s)
            paused = settled()
            time.sleep(.12)
            assert capture() == paused, "paused multi-activity playback kept moving"
            save_svg(folder / f"{binary.name}-two-profiles-paused-{width}x{height}.svg", width, height)
            send(" ")
            wait_for(lambda s: "Playing 120x" in s)
            send("\x0c")
            wait_for(lambda s: "Loaded files" in s)
            tmux("send-keys", "-t", "poc:0.0", "Home", "Enter")
            wait_for(lambda s: "Loaded files" not in s and "Short loop" in s and "Paused 1x" in s and "Elevation profiles" in s and "@ 0.00 km" in s)
            send(" ")
            wait_for(lambda s: "Playing 1x" in s)
            send("\x0c")
            wait_for(lambda s: "Loaded files" in s)
            send(" ")
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "Loaded files" not in s and "Free roam" in s and "Playing" not in s and "Elevation profile | 25.0–70.0 m" in s)
            send("e")
            wait_for(lambda s: "Elevation profile" not in s)
            send("e")
            wait_for(lambda s: "Elevation profile | 25.0–70.0 m" in s)
            send("q")
            wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
            print(f"{binary.name} {width}×{height}: ordered elevation, summed distance/cursor offsets, labelled selected playback, pause, activity switching, hiding a playing activity and remaining profile passed")
        finally:
            tmux("kill-session", "-t", "poc", check=False)


def quick_search(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx")])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "Riverside" in s and "Loading track file" not in s)
        for ascii_mode, coordinates, latitude in ((False, "51.62, -0.25", "51.62000"), (True, "51.63, -0.26", "51.63000")):
            if ascii_mode:
                send("a")
                wait_for(lambda s: not any(0x2800 <= ord(c) <= 0x28ff for c in s))
            before = settled()
            send("/qhjkl/as")
            wait_for(lambda s: "Quick search" in s and "qhjkl/as" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "Quick search" not in s)
            assert settled() == before, "cancelled quick search changed the map, selection or playback"
            send("/" + coordinates)
            wait_for(lambda s: "Quick search" in s and "51.6" in s)
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: "Quick search" not in s and latitude in s)
            screen = settled()
            assert "*" in "\n".join(screen.splitlines()[1:-4]), "searched coordinates have no map marker"
            assert "Planned journey" not in screen, "quick search created or selected a journey"
            send(" ")
            wait_context(lambda s: "Playing 1x" in s)
            until = time.monotonic() + .2
            wait_for(lambda s: time.monotonic() >= until and latitude in s)
            send(" ")
            wait_context(lambda s: "Paused 1x" in s and latitude in s)
            if ascii_mode:
                assert not any(0x2800 <= ord(c) <= 0x28ff for c in screen), "quick search reset ASCII mode"
            send("\x0bsearch")
            wait_for(lambda s: active_modal(s, "Commands") and "Quick place search" in s)
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: "Quick search" in s and "esc back" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: active_modal(s, "Commands") and "› search" in s)
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: "Commands" not in s and latitude in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: slash quick search, offline coordinates, input isolation, cancel restoration, preserved selection, nested command return, ASCII and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def favourite_controls(binary, width, height):
    with tempfile.TemporaryDirectory() as config:
        command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", f"XDG_CONFIG_HOME={config}",
                              "TERM=xterm-256color", "COLORTERM=truecolor", "python3",
                              str(ROOT / "scripts/terminal_runner.py"), str(binary)])
        saved = Path(config) / "mapbox-tui/favourites.json"
        for restart in (False, True):
            tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
            try:
                wait_for(lambda s: "No basemap" in s)
                if not restart:
                    send("/51.5, -0.1")
                    wait_for(lambda s: "Quick search" in s and "51.5" in s)
                    tmux("send-keys", "-t", "poc:0.0", "M-s")
                    wait_for(lambda s: "Save favourite" in s)
                    send("Home")
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Favourite saved" in s)
                    feedback = [run for run in styled_runs(capture(colors=True), width)
                                if "Favourite saved" in run[2]]
                    assert len(feedback) == 1 and feedback[0][3] == "#89b4fa", "feedback lost primary colour"
                    assert feedback[0][0] > width // 2 and feedback[0][1] > height // 2, "feedback is not bottom right"
                    save_svg(ROOT / "reports/generated" / f"{binary.name}-favourite-feedback-{width}x{height}.svg", width, height)
                    assert json.loads(saved.read_text())["favourites"][0]["name"] == "Home"
                    tmux("send-keys", "-t", "poc:0.0", "Escape")
                    wait_for(lambda s: "Quick search" not in s)
                else:
                    send("a/\x06")
                    wait_for(lambda s: "Favourites" in s and "Home" in s)
                    send("hom")
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Favourites" not in s and "51.50000" in s)
                    assert not any(0x2800 <= ord(c) <= 0x28ff for c in capture()), "favourite reset ASCII mode"
                    send("\x0bfavourite places")
                    wait_for(lambda s: active_modal(s, "Commands") and "Favourite places" in s)
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Favourites" in s and "Home" in s)
                    send("\x12")
                    wait_for(lambda s: "Rename favourite" in s)
                    tmux("send-keys", "-t", "poc:0.0", "C-e", "C-u")
                    send("Quiet home")
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Favourites" in s and "Quiet home" in s)
                    tmux("send-keys", "-t", "poc:0.0", "Delete")
                    wait_for(lambda s: "Delete favourite?" in s and "> Cancel" in s)
                    save_svg(ROOT / "reports/generated" / f"{binary.name}-favourite-confirm-{width}x{height}.svg", width, height)
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Favourites" in s and "Quiet home" in s)
                    assert len(json.loads(saved.read_text())["favourites"]) == 1
                    tmux("send-keys", "-t", "poc:0.0", "BSpace")
                    wait_for(lambda s: "Delete favourite?" in s and "> Cancel" in s)
                    tmux("send-keys", "-t", "poc:0.0", "Tab")
                    wait_for(lambda s: "> Delete" in s)
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Favourite deleted" in s)
                    assert json.loads(saved.read_text())["favourites"] == []
                    tmux("send-keys", "-t", "poc:0.0", "Escape")
                    wait_for(lambda s: active_modal(s, "Commands"))
                    tmux("send-keys", "-t", "poc:0.0", "Escape")
                    wait_for(lambda s: "Commands" not in s)
                send("q")
                wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
            finally:
                tmux("kill-session", "-t", "poc", check=False)
    print(f"{binary.name} {width}×{height}: save favourite, offline restart/reuse, rename, cancel/confirm delete, ASCII and terminal restoration passed")


def journey_controls(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary)])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "Free roam" in s)
        send("\x07")
        assert "Context · Files · Journey" not in settled(), "removed Journey Planner shortcut still opened the planner"
        send("\x0a")
        wait_for(lambda s: "Journey · Offline" in s and "A Start" in s)
        send("\x0a")
        assert "Context · Files · Journey" in settled(), "repeated shortcut closed planner"
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Context · Files · Journey" not in s)
        send("\x0a")
        wait_for(lambda s: "Context · Files · Journey" in s and "A Start" in s)
        tmux("send-keys", "-t", "poc:0.0", "Up")
        wait_for(lambda s: "> Copy journey to clipboard" in s)
        tmux("send-keys", "-t", "poc:0.0", "Down")
        wait_for(lambda s: "> A Start" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "A Start" in s and "Place or latitude" in s)
        tmux("send-keys", "-t", "poc:0.0", "M-m")
        wait_for(lambda s: "Context · Files · Journey" in s and "A Start: 0.000000, 0.000000" in s)

        def location(title, coordinates, editing=False):
            if not editing:
                tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: title in s and "enter coordinates" in s)
            tmux("send-keys", "-t", "poc:0.0", "C-u")
            if title == "B Destination":
                send(f"\x1b[200~{coordinates}\x1b[201~")
            else:
                send(coordinates)
            tmux("send-keys", "-t", "poc:0.0", "Enter")
            wait_for(lambda s: "Context · Files · Journey" in s and coordinates.split(",")[0] in s)

        tmux("send-keys", "-t", "poc:0.0", "BTab")
        wait_for(lambda s: "Loaded files" in s)
        tmux("send-keys", "-t", "poc:0.0", "Tab")
        wait_for(lambda s: "> A Start" in s)
        tmux("send-keys", "-t", "poc:0.0", "C-n")
        wait_for(lambda s: "Place or latitude" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "No waypoints" in s and "Place or latitude" not in s)
        tmux("send-keys", "-t", "poc:0.0", "Home")
        wait_for(lambda s: "> A Start" in s and "No waypoints" in s)
        location("A Start", "51.5, -0.1")
        tmux("send-keys", "-t", "poc:0.0", "Down", "Down")
        location("B Destination", "51.6, -0.2")
        tmux("send-keys", "-t", "poc:0.0", "Up")
        wait_for(lambda s: "> No waypoints" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        location("1 Waypoint", "51.55, -0.15", editing=True)
        tmux("send-keys", "-t", "poc:0.0", "Down")
        wait_for(lambda s: "> Add another waypoint" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        location("2 Waypoint", "51.56, -0.16", editing=True)
        send("\x12")
        wait_for(lambda s: "A Start: 51.600000" in s and "1 Waypoint: 51.560000" in s and "2 Waypoint: 51.550000" in s)
        tmux("send-keys", "-t", "poc:0.0", "Down", "Down", "Down", "Down")
        wait_for(lambda s: "> B Destination: 51.500000" in s)
        send("\x1a")
        tmux("send-keys", "-t", "poc:0.0", "Home")
        wait_for(lambda s: "A Start: 51.500000" in s and "1 Waypoint: 51.550000" in s)
        tmux("send-keys", "-t", "poc:0.0", "Down", "Down", "Down", "Down")
        wait_for(lambda s: "> B Destination: 51.600000" in s)
        tmux("send-keys", "-t", "poc:0.0", "Home")
        tmux("send-keys", "-t", "poc:0.0", "Down", "Down", "M-Up")
        wait_for(lambda s: "1 Waypoint: 51.560000" in s and "2 Waypoint: 51.550000" in s)
        tmux("send-keys", "-t", "poc:0.0", "Delete")
        wait_for(lambda s: "1 Waypoint: 51.550000" in s and "2 Waypoint:" not in s)
        folder = ROOT / "reports/generated"
        folder.mkdir(exist_ok=True, parents=True)
        screen = settled()
        assert "Journey · Offline" in screen and "A" in screen and "B" in screen, "planner hid the live map or current travel mode"
        save_svg(folder / f"{binary.name}-journey-sidebar-{width}x{height}.svg", width, height)
        tmux("send-keys", "-t", "poc:0.0", "C-b")
        wait_for(lambda s: "ctrl+b planner" in s)
        before = settled()
        tmux("send-keys", "-t", "poc:0.0", "Right")
        wait_for(lambda s: s != before and "ctrl+b planner" in s and "Context · Files · Journey" not in s)
        tmux("send-keys", "-t", "poc:0.0", "C-b")
        wait_for(lambda s: "ctrl+b hide" in s)
        before = settled()
        sgr(64, 10, 6)
        wait_for(lambda s: s != before and "Context · Files · Journey" in s)
        send("\x06")
        assert "Context · Files · Journey" in settled(), "removed Show shortcut still closed the planner"
        tmux("send-keys", "-t", "poc:0.0", "C-b")
        wait_for(lambda s: "Context · Files · Journey" not in s and "Planned journey" in s)
        screen = settled()
        assert "Playing" not in screen and "estimated 6 m/s" not in screen and "Elevation profile" not in screen, "planned journey became recorded playback"
        save_svg(folder / f"{binary.name}-journey-{width}x{height}.svg", width, height)
        send("a\x0a")
        wait_for(lambda s: "Context · Files · Journey" in s)
        tmux("send-keys", "-t", "poc:0.0", "Home")
        wait_for(lambda s: "Context · Files · Journey" in s and "A Start" in s)
        assert not any(0x2800 <= ord(c) <= 0x28ff for c in capture()), "ASCII journey rendered Braille"
        tmux("send-keys", "-t", "poc:0.0", "End", "Up", "Up", "Up", "Up", "Enter")
        wait_for(lambda s: "Travel mode" in s and "Offline" in s)
        tmux("send-keys", "-t", "poc:0.0", "Down", "Enter")
        wait_for(lambda s: "Mapbox not connected" in s)
        tmux("send-keys", "-t", "poc:0.0", "C-u")
        wait_for(lambda s: "Routing details" in s and "Ctrl-D retries" in s)
        settled()
        tmux("send-keys", "-t", "poc:0.0", "End")
        wait_for(lambda s: "docs/MAPBOX.md" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Travel mode" in s and "Walking" in s and "Mapbox not connected" in s)
        tmux("send-keys", "-t", "poc:0.0", "Home", "Enter")
        wait_for(lambda s: "Journey · Offline" in s)
        tmux("send-keys", "-t", "poc:0.0", "Home")
        wait_for(lambda s: "1 Waypoint:" in s)
        screen = settled().splitlines()
        row = next(index for index, line in enumerate(screen) if "1 Waypoint:" in line)
        column = screen[row].index("1 Waypoint:")
        sgr(0, column + 1, row + 1)
        sgr(0, column + 1, row + 1, release=True)
        wait_for(lambda s: "1 Waypoint" in s and "enter coordinates" in s)
        tmux("send-keys", "-t", "poc:0.0", "C-p")
        wait_for(lambda s: "Pick 1 Waypoint" in s)
        sgr(0, 8, 6)
        sgr(0, 8, 6, release=True)
        wait_for(lambda s: "Context · Files · Journey" in s and "1 Waypoint:" in s and "1 Waypoint: 51.550000" not in s)
        tmux("set-option", "-g", "set-clipboard", "on")
        tmux("send-keys", "-t", "poc:0.0", "End", "Enter")
        wait_for(lambda s: "Journey command sent to clipboard" in s)
        command = shlex.split(tmux("show-buffer"))
        assert command[0] == "mapbox-tui", command
        assert "--mode=offline" in command and not any(arg.startswith("--file=") for arg in command), command
        assert any(value.startswith("--waypoint=") for value in command), command
        assert "--start=51.5,-0.1" in command and "--end=51.6,-0.2" in command, command
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: docked planner/live map, keyboard focus, mouse zoom/point placement, waypoints, reversal/undo, ASCII, visible travel mode, routing feedback, offline fallback and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def select_mapbox_action(label):
    for _ in range(12):
        screen = settled()
        if "> " + label[:17] in screen:
            return
        tmux("send-keys", "-t", "poc:0.0", "Down")
    raise AssertionError(f"Mapbox action {label!r} was not reachable:\n{capture()}")


def mapbox_test_schema():
    operations = {
        "auth login": [], "auth logout": [], "auth whoami": [],
        "accounts list-tokens": ["--default", "--usage"], "accounts retrieve-token": [],
        "usage": ["--token-id", "--period-start", "--period-end"],
    }
    schema = json.dumps({
        "schema_version": 1,
        "global_options": [{"flag": flag, "values": ["text", "json"]}
                           for flag in ("--profile", "--use-login", "--output", "--timeout")],
        "commands": [{"command": "mapbox " + name,
                      "arguments": [{"flag": flag, "values": ["pk", "sk"] if flag == "--usage" else []}
                                    for flag in flags]}
                     for name, flags in operations.items()],
    })
    return schema


def auth_setup(binary):
    schema = mapbox_test_schema()
    with tempfile.TemporaryDirectory() as folder:
        folder = Path(folder)
        helper, marker = folder / "mapbox", folder / "terminal-handoff.json"
        helper.write_text(
            "#!/usr/bin/env python3\nimport json, os, sys\nfrom pathlib import Path\n"
            "if '--version' in sys.argv:\n    print('mapbox 0.3.0')\n"
            f"elif '--schema' in sys.argv:\n    print({schema!r})\n"
            "elif 'login' in sys.argv:\n"
            f"    Path({str(marker)!r}).write_text(json.dumps([os.isatty(i) for i in range(3)]))\n"
            "    print('Synthetic Mapbox login completed')\n"
            "else:\n    sys.exit(1)\n"
        )
        helper.chmod(0o755)
        for width, height in ((120, 40), (70, 18), (30, 12)):
            command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", f"MAPBOX_TUI_CLI={helper}",
                                  f"MAPBOX_CONFIG_DIR={folder / 'credentials'}", f"XDG_CONFIG_HOME={folder / 'settings'}", "TERM=xterm-256color",
                                  "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary),
                                  str(ROOT / "fixtures/example.gpx")])
            tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
            try:
                wait_for(lambda s: "Paused 1x" in s and "No basemap" in s)
                send("\x0bMapbox")
                wait_for(lambda s: "Mapbox account" in s)
                send("\r")
                wait_for(lambda s: "Mapbox setup" in s and "Check failed:" in s)
                select_mapbox_action("Sign in to Mapbox")
                wait_for(lambda s: "Mapbox setup" in s and "Sign in" in s)
                send("\r")
                if not (folder / "settings/mapbox-tui/settings.json").exists():
                    wait_for(lambda s: "billing risks" in s)
                    tmux("send-keys", "-t", "poc:0.0", "Tab", "Enter")
                wait_for(lambda s: "Mapbox setup" in s and marker.exists())
                assert json.loads(marker.read_text()) == [True, True, True], "login did not inherit terminal streams"
                send("\x1b")
                wait_for(lambda s: active_modal(s, "Commands") and "Mapbox account" in s and "Mapbox setup" not in s)
                send("\x1b")
                wait_for(lambda s: "Commands" not in s and "Paused 1x" in s)
                send("\x0c")
                wait_for(lambda s: "Loaded files" in s)
                tmux("send-keys", "-t", "poc:0.0", "Home")
                wait_for(lambda s: "Loaded files" in s and (width == 30 or "example.gpx" in s))
                send("\x1b")
                wait_for(lambda s: "Loaded files" not in s)
                send("q")
                wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
                transcript = re.sub(r"\s+", "", capture())
                notice = re.sub(r"\s+", "", "No basemap · Mapbox not connected")
                assert transcript.count(notice) == 1, "viewer did not echo a single concise connection notice"
                marker.unlink()
            finally:
                tmux("kill-session", "-t", "poc", check=False)
        for with_file in (False, True):
            pending = folder / f"pending-mapbox-{with_file}"
            args = [str(ROOT / "fixtures/example.gpx")] if with_file else []
            command = shlex.join(["env", "-u", "MAPBOX_ACCESS_TOKEN", f"MAPBOX_TUI_CLI={pending}",
                                  "TERM=xterm-256color", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), *args])
            tmux("new-session", "-d", "-s", "poc", "-x", "120", "-y", "40", command)
            try:
                if with_file:
                    screen = wait_for(lambda s: "Paused 1x" in s and "Mapbox not connected" in s)
                    assert "Mapbox setup" not in screen, "GPX startup opened an authentication prompt"
                    send("\x0bMapbox")
                    wait_for(lambda s: "Mapbox account" in s)
                    send("\r")
                screen = wait_for(lambda s: "Mapbox setup" in s and "Check again" in s)
                assert "Sign in" not in screen, "missing CLI offered a broken sign-in action"
                if not with_file:
                    screenshots = ROOT / "reports/generated"
                    screenshots.mkdir(parents=True, exist_ok=True)
                    save_svg(screenshots / f"{binary.name}-mapbox-setup-120x40.svg", 120, 40)
                select_mapbox_action("Installation help")
                send("\r")
                wait_for(lambda s: ("Installation ─" in s or "Installation -" in s) and "official installation instructions" in s)
                send("\x1b")
                wait_for(lambda s: "Mapbox setup" in s and "Check again" in s)
                if not with_file:
                    pending.write_bytes(helper.read_bytes())
                    pending.chmod(0o755)
                    select_mapbox_action("Check again")
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    select_mapbox_action("Sign in to Mapbox")
                else:
                    select_mapbox_action("Check again")
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    wait_for(lambda s: "Mapbox setup" in s and "Check again" in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: "Mapbox setup" not in s and "Mapbox not connected" in s)
                if with_file:
                    wait_for(lambda s: active_modal(s, "Commands") and "Mapbox account" in s)
                    send("\x1b")
                    wait_for(lambda s: "Commands" not in s and "Paused 1x" in s)
                send("q")
                wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
                assert capture().count("No basemap · Mapbox not connected") == 1, "missing CLI notice wasn't echoed once"
            finally:
                tmux("kill-session", "-t", "poc", check=False)
    print(f"{binary.name}: dependency-aware setup actions, installation help/recheck, palette login, stdout notice and terminal restoration passed")


def billing_usage(binary):
    with tempfile.TemporaryDirectory() as directory:
        folder = Path(directory)
        helper, calls, link = folder / "mapbox", folder / "calls.jsonl", folder / "link.txt"
        helper.write_text(
            "#!/usr/bin/env python3\nimport json, sys\nfrom pathlib import Path\n"
            "from datetime import datetime, timezone\n"
            "args = sys.argv[1:]\n"
            "if '--version' in args:\n    print('mapbox 0.3.0'); sys.exit(0)\n"
            f"if '--schema' in args:\n    print({mapbox_test_schema()!r}); sys.exit(0)\n"
            f"with Path({str(calls)!r}).open('a') as file:\n    file.write(json.dumps(args) + '\\n')\n"
            "if 'whoami' in args:\n    print(json.dumps({'account': 'fixtureaccount'}))\n"
            "elif 'usage' in args:\n"
            "    start = args[args.index('--period-start') + 1]\n"
            "    end = args[args.index('--period-end') + 1]\n"
            "    counts = {'Vector Tiles API': 123, 'Search Box API - Sessions': 8, 'Directions API': 3, "
            "'Permanent Geocoding API': 2, 'Map Loads for Web': 999}\n"
            "    products = {name: {'daily': [{'date': end, 'usage': total}]} for name, total in counts.items()}\n"
            "    print(json.dumps({'data': {'period': {'start': start, 'end': end}, 'products': products}, "
            "'generated_at': datetime.now(timezone.utc).isoformat()}))\n"
            "else:\n    sys.exit(1)\n"
        )
        helper.chmod(0o755)
        opener = folder / "xdg-open"
        opener.write_text("#!/usr/bin/env python3\nimport sys\nfrom pathlib import Path\n"
                          f"Path({str(link)!r}).write_text(sys.argv[1])\n")
        opener.chmod(0o755)
        for width, height in ((120, 40), (70, 24), (30, 12)):
            config = folder / f"settings-{width}"
            settings = config / "mapbox-tui/settings.json"
            environment = ["env", "MAPBOX_ACCESS_TOKEN=pk.fixture", "MAPBOX_TUI_ACCEPT_BILLING=",
                           f"MAPBOX_TUI_CLI={helper}", f"XDG_CONFIG_HOME={config}",
                           f"PATH={folder}:{os.environ['PATH']}", "HTTPS_PROXY=http://127.0.0.1:1",
                           "TERM=xterm-256color", "COLORTERM=truecolor"]
            command = shlex.join([*environment, "python3", str(ROOT / "scripts/terminal_runner.py"),
                                  str(binary), str(ROOT / "fixtures/example.gpx")])
            for launch in range(2):
                before = len(calls.read_text().splitlines()) if calls.exists() else 0
                tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
                try:
                    if launch == 0:
                        wait_for(lambda screen: "billing risks" in screen and "Continue offline" in screen)
                        if width >= 120:
                            assert "Accept risks and continue online" in capture()
                        save_svg(ROOT / "reports/generated" / f"mapbox-tui-billing-{width}x{height}.svg", width, height)
                        send("\r")
                    wait_for(lambda screen: "Offline maps" in screen and "billing risks" not in screen)
                    assert json.loads(settings.read_text())["offline"], "offline choice was not saved"
                    assert not json.loads(settings.read_text())["mapbox_billing_accepted"], "offline accepted billing risks"
                    send("\x0bMapbox\r")
                    wait_for(lambda screen: "Mapbox settings" in screen)
                    # Both launches fetch account usage, including the persisted-offline launch.
                    deadline = time.monotonic() + 5
                    while time.monotonic() < deadline:
                        recorded = [json.loads(line) for line in calls.read_text().splitlines()[before:]] if calls.exists() else []
                        if any("usage" in args for args in recorded):
                            break
                        time.sleep(.03)
                    assert sum("usage" in args for args in recorded) == 1, "startup usage missing or repeated"
                    assert all("whoami" in args or "usage" in args for args in recorded), recorded
                    if width >= 70:
                        wait_for(lambda screen: "Vector Tiles API: 123" in screen
                                 and "Usage: fixtureaccount (saved login)" in screen
                                 and "account may differ" in screen)
                        assert "Map Loads for Web" not in capture(), "unrelated product was displayed"
                    save_svg(ROOT / "reports/generated" / f"mapbox-tui-usage-menu-{width}x{height}.svg", width, height)
                    select_mapbox_action("Usage details")
                    send("\r")
                    wait_for(lambda screen: "Mapbox usage" in screen)
                    if width >= 70:
                        wait_for(lambda screen: "123 requests" in screen and "days reported" in screen
                                 and "Usage: fixtureaccount (saved login)" in screen
                                 and "account may differ" in screen)
                    save_svg(ROOT / "reports/generated" / f"mapbox-tui-usage-{width}x{height}.svg", width, height)
                    send("\r")
                    wait_for(lambda screen: link.exists())
                    assert link.read_text() == "https://console.mapbox.com/account/statistics/"
                    link.unlink()
                    send("\x1b")
                    wait_for(lambda screen: "Mapbox settings" in screen and "> Usage details" in screen)
                    send("\x1b")
                    wait_for(lambda screen: active_modal(screen, "Commands"))
                    send("\x1b")
                    wait_for(lambda screen: "Commands" not in screen)
                    send("/")
                    wait_for(lambda screen: "Quick search" in screen)
                    if height >= 24:
                        wait_for(lambda screen: "Go online in Mapbox settings" in screen)
                    send("51.3,-2.5\r")
                    wait_for(lambda screen: "Quick search" not in screen and "Offline maps" in screen)
                    send("q")
                    wait_for(lambda screen: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", screen))
                finally:
                    tmux("kill-session", "-t", "poc", check=False)
    print(f"{binary.name}: billing confirmation, persisted offline mode, startup usage, product filtering, web link and offline coordinates passed")


def traffic_menu(binary):
    with tempfile.TemporaryDirectory() as folder:
        folder = Path(folder)
        for width, height in ((120, 40), (70, 18), (30, 12)):
            command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", f"MAPBOX_TUI_CLI={folder / 'missing'}",
                                  f"XDG_CONFIG_HOME={folder}", "TERM=xterm-256color", "COLORTERM=truecolor",
                                  "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary),
                                  str(ROOT / "fixtures/example.gpx")])
            tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
            try:
                wait_for(lambda s: "Paused 1x" in s and "No basemap" in s)
                send("\x0btraffic")
                wait_for(lambda s: active_modal(s, "Commands") and "Toggle traffic" in s)
                if width >= 70:
                    assert "[ ]" not in capture(), "traffic menu still used a checkbox prefix"
                traffic_row = next(row for row in capture(colors=True).splitlines() if "Toggle traffic" in row)
                assert "38;2;108;112;134" in traffic_row, "unverified traffic was not greyed out"
                tmux("send-keys", "-t", "poc:0.0", "Down")
                send(" ")
                wait_for(lambda s: active_modal(s, "Commands") and "Verify Mapbox" in s)
                send("\r")
                wait_for(lambda s: active_modal(s, "Commands") and "Verify Mapbox" in s)
                settings = folder / "mapbox-tui/settings.json"
                assert not settings.exists(), "disabled traffic changed the saved preference"
                send("\x1b")
                wait_for(lambda s: "Commands" not in s and "Paused 1x" in s)
                send("a")
                wait_for(lambda s: not any(0x2800 <= ord(c) <= 0x28ff for c in s))
                send("\x0btraffic")
                wait_for(lambda s: active_modal(s, "Commands") and "Toggle traffic" in s)
                send("\x1b")
                screen = wait_for(lambda s: "Commands" not in s)
                assert "Paused 1x" in screen, "traffic controls discarded the imported activity"
                send("q")
                wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
            finally:
                tmux("kill-session", "-t", "poc", check=False)
    print(f"{binary.name}: main-menu traffic entry, greyed-out unverified access, disabled action, ASCII, compact layouts and preserved imports passed")


def nested_modals(binary):
    with tempfile.TemporaryDirectory() as folder:
        for width, height in ((120, 40), (70, 18), (30, 12)):
            command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", f"MAPBOX_TUI_CLI={folder}/missing",
                                  "TERM=xterm-256color", "COLORTERM=truecolor", "python3",
                                  str(ROOT / "scripts/terminal_runner.py"), str(binary),
                                  str(ROOT / "fixtures/example.gpx")])
            tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
            try:
                wait_for(lambda s: "Paused 1x" in s)
                send("\x0bMapbox\r")
                wait_for(lambda s: "Mapbox setup" in s and "Check failed:" in s)
                select_mapbox_action("Installation help")
                send("\r")
                screen = wait_for(lambda s: ("Installation ─" in s or "Installation -" in s) and "esc back" in s)
                assert "› Installation" in screen and screen.count("╭") <= 2, f"nested modal lost breadcrumbs or added parent frames:\n{screen}"
                save_svg(ROOT / "reports/generated" / f"{binary.name}-modal-stack-{width}x{height}.svg", width, height)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: "Mapbox setup" in s and "Installation ─" not in s and "Installation -" not in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: active_modal(s, "Commands") and "› Mapbox" in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: "Commands" not in s)
                send("a\x0bMapbox\r")
                wait_for(lambda s: "Mapbox setup" in s and "Check failed:" in s)
                select_mapbox_action("Installation help")
                send("\r")
                wait_for(lambda s: ("Installation ─" in s or "Installation -" in s) and "esc back" in s and "╭" not in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: "Mapbox setup" in s and "Installation ─" not in s and "Installation -" not in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: active_modal(s, "Commands") and "› Mapbox" in s)
                tmux("send-keys", "-t", "poc:0.0", "Escape")
                wait_for(lambda s: "Commands" not in s)
                send("q")
                wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
            finally:
                tmux("kill-session", "-t", "poc", check=False)
    print(f"{binary.name}: breadcrumb modal titles, differing sizes, ASCII, filtered-menu return and compact layouts passed")


if __name__ == "__main__":
    settings = tempfile.TemporaryDirectory()
    os.environ["XDG_CONFIG_HOME"] = settings.name
    if "--mapbox" not in sys.argv:
        # Startup statistics must not touch the developer's real account.
        os.environ["MAPBOX_TUI_CLI"] = str(Path(settings.name) / "missing-mapbox")
        os.environ.pop("MAPBOX_TUI_ACCEPT_BILLING", None)
    try:
        tmux("start-server", ";", "set-option", "-g", "exit-empty", "off")
        for name in ("mapbox-tui",):
            free_roam(ROOT / "bin" / name, live="--mapbox" in sys.argv)
            if "--mapbox" not in sys.argv:
                tiny_start(ROOT / "bin" / name)
            for width, height in ((120,40), (160,50)):
                if "--mapbox" in sys.argv:
                    live_capture(ROOT / "bin" / name, width, height)
                else:
                    scenario(ROOT / "bin" / name, width, height)
                    profile_playback(ROOT / "bin" / name, width, height)
            if "--mapbox" in sys.argv:
                live_playback(ROOT / "bin" / name)
            else:
                auth_setup(ROOT / "bin" / name)
                billing_usage(ROOT / "bin" / name)
                traffic_menu(ROOT / "bin" / name)
                nested_modals(ROOT / "bin" / name)
                dense_profile(ROOT / "bin" / name)
                dialogs(ROOT / "bin" / name)
                for width, height in ((120, 40), (70, 24)):
                    theme_switching(ROOT / "bin" / name, width, height)
                    command_palette_access(ROOT / "bin" / name, width, height)
                workspace_controls(ROOT / "bin" / name)
                for width, height in ((120, 40), (70, 24), (70, 18)):
                    workspace_profiles(ROOT / "bin" / name, width, height)
                    journey_controls(ROOT / "bin" / name, width, height)
                for width, height in ((120, 40), (70, 18), (30, 12)):
                    quick_search(ROOT / "bin" / name, width, height)
                for width, height in ((120, 40), (70, 18)):
                    favourite_controls(ROOT / "bin" / name, width, height)
    finally:
        tmux("kill-server", check=False)
        settings.cleanup()
