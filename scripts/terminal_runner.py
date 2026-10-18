import os
from pathlib import Path
import subprocess
import sys
import termios
import time

before = termios.tcgetattr(0)
process = subprocess.Popen(sys.argv[1:])
if pid_file := os.environ.get("MAPBOX_TUI_TEST_PID_FILE"):
    Path(pid_file).write_text(str(process.pid))
returncode = process.wait()
after = termios.tcgetattr(0)
print(f"TERMINAL_RESTORED={before == after} EXIT={returncode}", flush=True)
time.sleep(15)
