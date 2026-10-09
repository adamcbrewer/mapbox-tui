import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def main():
    env = dict(os.environ)
    env.pop("MAPBOX_ACCESS_TOKEN", None)
    result = subprocess.run(
        [str(ROOT / "bin/shoe"), "auth", "status", "--usage", "--json"],
        env=env, capture_output=True, text=True, timeout=75,
    )
    if result.returncode != 0:
        raise ValueError("shoe could not produce an authentication report")
    report = json.loads(result.stdout)
    if any(prefix in result.stdout for prefix in ("pk.", "sk.", "tk.")):
        raise ValueError("authentication report contains a token prefix")
    print(json.dumps(report, indent=2))
    ready = report["login"]["state"] == "usable" and report["rendering"]["validity"] == "valid"
    return 0 if ready and report["rendering"]["streets"]["state"] == "ready" else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError, subprocess.TimeoutExpired):
        print("Live authentication check failed; no raw token-bearing responses printed.", file=sys.stderr)
        sys.exit(1)
