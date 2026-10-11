from pathlib import Path
import signal
import subprocess
import sys
import termios
import time

ROOT = Path(__file__).resolve().parents[1]


def snapshot():
    paths = [ROOT / "go.mod", ROOT / "go.sum", ROOT / "shared/tools.json", ROOT / "Makefile"]
    for folder in ("go", "shared"):
        paths.extend((ROOT / folder).rglob("*.go"))
    paths.extend((ROOT / "go/themes").glob("*.toml"))
    result = {}
    for path in paths:
        try:
            stat = path.stat()
            result[path] = (stat.st_mtime_ns, stat.st_size)
        except FileNotFoundError:
            pass
    return result


def stop(process):
    if process is not None and process.poll() is None:
        process.terminate()
        process.wait()


def interrupt(signum, frame):
    raise KeyboardInterrupt


def main():
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        print("Run make dev in an interactive terminal.", file=sys.stderr)
        return 1
    args = sys.argv[1:] or ["fixtures/example.gpx"]
    before = termios.tcgetattr(sys.stdin)
    signal.signal(signal.SIGTERM, interrupt)
    process = None
    previous = snapshot()
    changed = None
    rebuild = True
    print("[dev] Watching Go files and build inputs. q or Ctrl-C stops the watcher.", flush=True)
    try:
        while True:
            if process is not None and process.poll() is not None:
                return process.returncode
            current = snapshot()
            if current != previous:
                previous = current
                changed = time.monotonic()
            if rebuild or (changed is not None and time.monotonic() - changed >= 0.3):
                stop(process)
                process = None
                termios.tcsetattr(sys.stdin, termios.TCSADRAIN, before)
                print("[dev] Building mapbox-tui…", flush=True)
                result = subprocess.run(["make", "build"], cwd=ROOT)
                rebuild = False
                changed = None
                if result.returncode == 0:
                    print("[dev] Starting mapbox-tui…", flush=True)
                    process = subprocess.Popen([str(ROOT / "bin/mapbox-tui"), *args], cwd=ROOT)
                else:
                    print("[dev] Build failed. Waiting for the next change…", flush=True)
            time.sleep(0.2)
    except KeyboardInterrupt:
        return 0
    finally:
        stop(process)
        termios.tcsetattr(sys.stdin, termios.TCSADRAIN, before)


if __name__ == "__main__":
    sys.exit(main())
