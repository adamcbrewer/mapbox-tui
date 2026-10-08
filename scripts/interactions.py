from pathlib import Path
import difflib
import html
import os
import re
import shlex
import subprocess
import time
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SOCKET = f"shoe-prototype-{os.getpid()}"
ANSI = re.compile(r"\x1b\[([0-9;]*)m")


def tmux(*args, check=True):
    return subprocess.run(["tmux", "-L", SOCKET, "-f", "/dev/null", *args], capture_output=True, text=True, check=check).stdout


def capture(colors=False):
    text = tmux("capture-pane", "-p", *( ["-e"] if colors else []), "-t", "poc:0.0")
    return text if colors else text.expandtabs(8)


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


def save_svg(path, width, height, background="#1e1e2e", foreground="#cdd6f4"):
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width*9}" height="{height*18}" viewBox="0 0 {width*9} {height*18}">', f'<rect width="100%" height="100%" fill="{background}"/>', '<g font-family="DejaVu Sans Mono,monospace" font-size="15" xml:space="preserve">']
    fg = foreground
    bg = background
    for y, line in enumerate(capture(colors=True).splitlines()):
        x, position = 0, 0
        for match in [*ANSI.finditer(line), None]:
            end = match.start() if match else len(line)
            chunk = line[position:end]
            if chunk:
                expanded, column = [], x
                for char in chunk:
                    if char == "\t":
                        spaces = 8 - column % 8
                        expanded.append(" " * spaces)
                        column += spaces
                    else:
                        expanded.append(char)
                        column += 1
                chunk = "".join(expanded)
                if bg != background:
                    out.append(f'<rect x="{x*9}" y="{y*18}" width="{len(chunk)*9}" height="18" fill="{bg}"/>')
                out.append(f'<text x="{x*9}" y="{y*18+14}" fill="{fg}">{html.escape(chunk)}</text>')
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
    out.extend(['</g>', '</svg>'])
    path.write_text('\n'.join(out) + '\n')
    subprocess.run(["rsvg-convert", "-o", str(path.with_suffix('.png')), str(path)], check=True)


def scenario(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx")])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "Route details" in s and "Elevation profile" in s and "Loading basemap" not in s)
        folder = ROOT / "reports/generated"
        folder.mkdir(exist_ok=True, parents=True)
        save_svg(folder / f"{binary.name}-{width}x{height}.svg", width, height)
        initial = capture()
        assert "No basemap" in initial and "token missing" not in initial, "token-free GPX viewing did not use a quiet empty canvas"
        zoom = re.search(r"Zoom ([\d.]+)", initial).group(1)
        send("]")
        wait_for(lambda s: "Point 2 / 10" in s)
        send(".")
        wait_for(lambda s: "Bearing 10" in s)
        send("n")
        wait_for(lambda s: "Bearing 0°" in s)
        sgr(2, 30, 15); sgr(34, 35, 15); sgr(2, 35, 15, release=True)
        wait_for(lambda s: "Bearing 10" in s)
        send("n")
        wait_for(lambda s: "Bearing 0°" in s)
        sgr(64, 30, 15)
        single = wait_for(lambda s: re.search(r"Zoom ([\d.]+)", s).group(1) != zoom)
        single_zoom = float(re.search(r"Zoom ([\d.]+)", single).group(1))
        assert abs(single_zoom - float(zoom) - .1) < .011, "wheel step differs from 0.1"
        send("+")
        keyboard = wait_for(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > single_zoom)
        keyboard_zoom = float(re.search(r"Zoom ([\d.]+)", keyboard).group(1))
        assert abs(keyboard_zoom - single_zoom - .1) < .011, "keyboard step differs from 0.1"
        send("\x1b[<64;30;15M" * 8)
        burst = wait_for(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > keyboard_zoom)
        burst_zoom = float(re.search(r"Zoom ([\d.]+)", burst).group(1))
        assert burst_zoom - keyboard_zoom < .4, "rapid wheel burst jumped multiple levels"
        before_modified_zoom = float(re.search(r"Zoom ([\d.]+)", settled()).group(1))
        sgr(80, 30, 15)
        modified = wait_for(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > before_modified_zoom)
        modified_zoom = float(re.search(r"Zoom ([\d.]+)", modified).group(1))
        assert abs(modified_zoom - before_modified_zoom - .2) < .011, "Ctrl+wheel did not double the zoom step"
        before_horizontal = capture()
        send("\x1b[<66;30;15M\x1b[<67;30;15M")
        send("v")
        wait_for(lambda s: "Provider credits" in s)
        send("v")
        horizontal = wait_for(lambda s: "Provider credits" not in s)
        assert re.search(r"Zoom ([\d.]+)", horizontal).group(1) == re.search(r"Zoom ([\d.]+)", before_horizontal).group(1), "horizontal wheel zoomed the map"
        send("f")
        wait_for(lambda s: f"Zoom {zoom}" in s)
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
                wait_for(lambda s: any(label in s for label in ("Point 1 / 10", "Point 7 / 10", "Point 1 / 2")))
                break
        else:
            raise AssertionError("start marker not visible")
        send("a")
        wait_for(lambda s: not any(0x2800 <= ord(c) <= 0x28ff for c in s))
        send("v")
        wait_for(lambda s: "Provider credits" in s and "No basemap" in s)
        send("v")
        tmux("resize-window", "-t", "poc:0", "-x", "70", "-y", "24")
        wait_for(lambda s: "[ ] select; PgUp/PgDn" in s)
        tmux("resize-window", "-t", "poc:0", "-x", "25", "-y", "10")
        wait_for(lambda s: "Enlarge terminal" in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: keyboard, SGR wheel/pan/click/right-drag, north reset, ASCII, narrow/tiny resize, clean exit passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def live_capture(binary, width, height):
    command = shlex.join(["env", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx"), "--provider", "mapbox", "--center-lon", "-0.1065", "--center-lat", "51.505", "--zoom", "13", "--point-index", "3"])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        screen = wait_for(lambda s: "Route details" in s and "© Mapbox" in s and "Loading basemap" not in s and re.search(r"\b[1-9]\d* / 0 / 0", s), timeout=25)
        folder = ROOT / "reports/generated"
        folder.mkdir(exist_ok=True, parents=True)
        save_svg(folder / f"{binary.name}-mapbox-{width}x{height}.svg", width, height)
        stats = re.search(r"(\d+ / \d+ / \d+)", screen).group(1)
        send("]")
        screen = wait_for(lambda s: "Point 5 / 10" in s)
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
        wait_for(lambda s:"Route details" in s)
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
        wait_for(lambda s:"Route details" in s and "Elevation profile" in s and "Loading basemap" not in s)
        wait_for(lambda s:"Elevation profile" in s and "@ 0.00 km" in s)
        send("e")
        wait_for(lambda s:"Elevation profile" not in s)
        send("e")
        wait_for(lambda s:"Elevation profile" in s and "@ 0.00 km" in s)
        send("ssss")
        wait_for(lambda s:"Paused 120x" in s)
        send(" ")
        wait_for(lambda s:"Playing 120x" in s and "Free camera" in s and any(float(value)>.1 for value in re.findall(r"@ ([\d.]+) km",s)))
        send("c")
        wait_for(lambda s:"Playing 120x" in s and "Following position" in s)
        send(" ")
        wait_for(lambda s:"Paused 120x" in s)
        paused=settled()
        assert "Between " in paused and "~" in paused, "playback did not interpolate recorded samples"
        time.sleep(.12)
        assert capture()==paused, "pause kept changing ride position"
        folder=ROOT / "reports/generated"
        folder.mkdir(exist_ok=True,parents=True)
        save_svg(folder / f"{binary.name}-profile-{width}x{height}.svg",width,height)
        send("S")
        wait_for(lambda s:"Paused 60x" in s)
        tmux("send-keys","-t","poc:0.0","End")
        wait_for(lambda s:"Point 10 / 10" in s and "Finished 60x" in s)
        send(" ")
        wait_for(lambda s:"Playing 60x" in s and "Between 1–2" in s)
        send("h")
        wait_for(lambda s:"Playing 60x" in s and "Free camera" in s)
        send(" ")
        wait_for(lambda s:"Paused 60x" in s)
        tmux("resize-window","-t","poc:0","-x","70","-y","24")
        wait_for(lambda s:"Elevation profile" in s and "[ ] select; PgUp/PgDn" in s)
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
    command=shlex.join(["env","TERM=xterm-256color","COLORTERM=truecolor","python3",str(ROOT / "scripts/terminal_runner.py"),str(binary),"open",str(ROOT / "fixtures/example.gpx"),"--provider","mapbox","--zoom","14","--point-index","3"])
    tmux("new-session","-d","-s","poc","-x","120","-y","40",command)
    try:
        wait_for(lambda s:"Route details" in s and "Loading basemap" not in s and re.search(r"\b[1-9]\d* / 0 / 0",s),timeout=25)
        before=wait_for(lambda s:"Elevation profile" in s)
        base_hits=int(re.search(r"(\d+) / (\d+) / (\d+)",before).group(2))
        send("ssss ")
        wait_for(lambda s:"Playing 120x" in s and "Free camera" in s)
        send("c")
        def moving(s):
            stats=re.search(r"(\d+) / (\d+) / (\d+)",s)
            return "Playing 120x" in s and stats and int(stats.group(2))>base_hits and int(stats.group(3))==0
        screen=wait_for(moving,timeout=10)
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
        wait_for(lambda s:"100000 points" in s)
        wait_for(lambda s:"Elevation profile" in s)
        send("ss ")
        wait_for(lambda s:"Playing 20x" in s and re.search(r"Playing 20x\s+00:0[5-9]",s))
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
        wait_for(lambda s: "Map details" in s and "Free roam" in s)
        send("\x0f")
        wait_for(lambda s: "Open track file" in s and "› bin" in s)
        send("jj")
        wait_for(lambda s: "› fixtures" in s)
        tmux("send-keys", "-t", "poc:0.0", "Right")
        wait_for(lambda s: "example.gpx" in s and "/fixtures" in s)
        folder = ROOT / "reports/generated"
        save_svg(folder / f"{binary.name}-filepicker-120x40.svg", 120, 40)

        def select_file(name):
            tmux("send-keys", "-t", "poc:0.0", "g")
            for _ in range(20):
                current = capture()
                if re.search(r"›\s+" + re.escape(name), current):
                    tmux("send-keys", "-t", "poc:0.0", "Enter")
                    return
                send("j")
                wait_for(lambda s: s != current)
            raise AssertionError(f"File picker did not reach {name}")

        select_file("example.gpx")
        wait_for(lambda s: "Route details" in s and "Open track file" not in s)
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
        send("\x10")
        wait_for(lambda s: "Commands" in s and "Type a command" in s)
        send("speed")
        wait_for(lambda s: "Playback speed" in s and "Open track file" not in s)
        save_svg(folder / f"{binary.name}-commands-120x40.svg", 120, 40)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Playback speed multiplier" in s)
        save_svg(folder / f"{binary.name}-speed-120x40.svg", 120, 40)
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
        wait_for(lambda s: "No track points" in s and "Open track file" not in s)
        send("\x10")
        wait_for(lambda s: "Commands" in s and "Type a command" in s)
        send("free roam")
        wait_for(lambda s: "Free roam (clear selection)" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Map details" in s and "Commands" not in s and "Elevation profile" in s)
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
        initial = wait_for(lambda s: "Map details" in s and "Free roam" in s and "Loading basemap" not in s, timeout=25)
        assert "Route details" not in initial and "Elevation profile" not in initial and "Paused" not in initial
        if live:
            assert "© Mapbox" in initial and re.search(r"\b[1-9]\d* / 0 / 0", initial), initial
        else:
            assert "No basemap" in initial and "MAPBOX_ACCESS_TOKEN" in initial, "empty-canvas startup did not show Mapbox setup guidance"
        before = settled()
        send("h")
        wait_for(lambda s: re.search(r"Lon (-?[\d.]+)", s).group(1) != re.search(r"Lon (-?[\d.]+)", before).group(1))
        sgr(0, 30, 15); sgr(32, 35, 16); sgr(0, 35, 16, release=True)
        sgr(64, 30, 15)
        wait_for(lambda s: re.search(r"Zoom ([\d.]+)", s).group(1) != re.search(r"Zoom ([\d.]+)", before).group(1))
        current = settled()
        zoom = float(re.search(r"Zoom ([\d.]+)", current).group(1))
        sgr(80, 30, 15)
        modified = wait_for(lambda s: float(re.search(r"Zoom ([\d.]+)", s).group(1)) > zoom)
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
        wait_for(lambda s: "Free roam | zoom" in s and "ctrl+l loaded files" in s)
        send("q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name}: {'live Mapbox' if live else 'offline'} free roam, keyboard/mouse navigation, coordinate inspection, help, compact resize and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def theme_switching(binary, width, height):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx"), "--theme", "catppuccin"])
    tmux("new-session", "-d", "-s", "poc", "-x", str(width), "-y", str(height), command)
    try:
        wait_for(lambda s: "Elevation profile" in s and "Paused 1x" in s)
        baseline = settled()
        original_colors = capture(colors=True)
        send("\x10")
        wait_for(lambda s: "Commands" in s)
        send("theme")
        wait_for(lambda s: "Choose colour theme" in s)
        tmux("send-keys", "-t", "poc:0.0", "Enter")
        wait_for(lambda s: "Colour theme" in s and "Catppuccin (current)" in s)
        for name, background, foreground in (("Tokyo Night", "#1a1b26", "#a9b1d6"), ("Catppuccin Latte", "#eff1f5", "#4c4f69"), ("Catppuccin", "#1e1e2e", "#cdd6f4")):
            send("\x14")
            wait_for(lambda s: f"{name} (current)" in s)
            save_svg(ROOT / "reports/generated" / f"{binary.name}-theme-{name.lower().replace(' ', '-')}-{width}x{height}.svg", width, height, background, foreground)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Colour theme" not in s)
        assert settled() == baseline, "theme picker changed geometry, selection or playback"
        send("\x14")
        wait_for(lambda s: capture(colors=True) != original_colors)
        assert settled() == baseline, "palette change affected map text or layout"
        for key, label in (("?", "Keyboard shortcuts"), ("v", "Provider credits"), ("p", "Playback speed multiplier"), ("\x0c", "Loaded files"), ("\x0f", "Open track file"), ("\x10", "Commands")):
            send(key)
            wait_for(lambda s: label in s)
            if label == "Commands":
                send("theme")
                wait_for(lambda s: "Choose colour theme" in s)
            before = settled()
            colors = capture(colors=True)
            send("\x14")
            wait_for(lambda s: capture(colors=True) != colors)
            assert settled() == before, f"theme cycling reset the open {label} dialog"
            tmux("send-keys", "-t", "poc:0.0", "Escape")
            wait_for(lambda s: label not in s)
        send("a ")
        wait_for(lambda s: "Playing 1x" in s and not any(0x2800 <= ord(c) <= 0x28ff for c in s))
        send("p\x14")
        wait_for(lambda s: "Playback speed multiplier" in s and "Playing 1x" in s)
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "Playback speed multiplier" not in s and "Playing 1x" in s)
        send(" q")
        wait_for(lambda s: "TERMINAL_RESTORED=TrueEXIT=0" in re.sub(r"\s+", "", s))
        print(f"{binary.name} {width}×{height}: three themes, modal/widget recolouring, unchanged geometry, ASCII playback and terminal restoration passed")
    finally:
        tmux("kill-session", "-t", "poc", check=False)


def workspace_controls(binary):
    command = shlex.join(["env", "MAPBOX_ACCESS_TOKEN=", "TERM=xterm-256color", "COLORTERM=truecolor", "python3", str(ROOT / "scripts/terminal_runner.py"), str(binary), str(ROOT / "fixtures/example.gpx"), str(ROOT / "fixtures/waypoints.gpx")])
    tmux("new-session", "-d", "-s", "poc", "-x", "120", "-y", "40", command)
    try:
        wait_for(lambda s: "2 files" in s and "No track points" in s and "Loading track file" not in s)
        send("\x0c")
        wait_for(lambda s: "Loaded files" in s and "example.gpx" in s and "waypoints.gpx" in s)
        save_svg(ROOT / "reports/generated" / f"{binary.name}-workspace-120x40.svg", 120, 40)
        send(" ")
        tmux("send-keys", "-t", "poc:0.0", "Escape")
        wait_for(lambda s: "No active item" in s and "Loaded files" not in s)
        send("\x0c")
        wait_for(lambda s: "Loaded files" in s)
        tmux("send-keys", "-t", "poc:0.0", "End")
        send("D")
        wait_for(lambda s: "Loaded files" in s and "example.gpx" in s and "waypoints.gpx" not in s)
        tmux("send-keys", "-t", "poc:0.0", "Home", "Enter")
        wait_for(lambda s: "Loaded files" not in s and "Riverside exploration" in s and "Elevation profile" in s)
        send("f")
        settled()
        tmux("resize-window", "-t", "poc:0", "-x", "70", "-y", "24")
        send("\x0c")
        wait_for(lambda s: "Loaded files" in s and "Optional spur" in s)
        tmux("send-keys", "-t", "poc:0.0", "End", "Enter")
        wait_for(lambda s: "Loaded files" not in s and "Optional spur" in s)
        send("\x10")
        wait_for(lambda s: "Commands" in s)
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
            wait_for(lambda s: "2:1 Long ride | Playing 120x" in s and any(float(value) > start_distance + .01 for value in re.findall(r"@ ([\d.]+) km", s)))
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
            wait_for(lambda s: "Loaded files" not in s and "1:1 Short loop | Paused 1x" in s and "Elevation profiles" in s and "@ 0.00 km" in s)
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


if __name__ == "__main__":
    try:
        for name in ("shoe",):
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
                dense_profile(ROOT / "bin" / name)
                dialogs(ROOT / "bin" / name)
                for width, height in ((120, 40), (70, 24)):
                    theme_switching(ROOT / "bin" / name, width, height)
                workspace_controls(ROOT / "bin" / name)
                for width, height in ((120, 40), (70, 24), (70, 18)):
                    workspace_profiles(ROOT / "bin" / name, width, height)
    finally:
        tmux("kill-server", check=False)
