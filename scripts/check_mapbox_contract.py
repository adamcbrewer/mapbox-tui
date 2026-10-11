import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import shutil
import sys

COMMANDS = {
    ("auth", "login"): set(),
    ("auth", "logout"): set(),
    ("auth", "whoami"): {"--verify"},
    ("accounts", "list-tokens"): {"--default", "--usage", "--username"},
    ("accounts", "retrieve-token"): set(),
    ("usage",): {"--token-id", "--period-start", "--period-end"},
}


def check(executable):
    env = dict(os.environ, MAPBOX_CLI_NO_TELEMETRY="1", MAPBOX_NO_UPDATE_CHECK="1")
    result = subprocess.run([str(executable), "--version"], env=env, capture_output=True, text=True, timeout=10)
    match = re.fullmatch(r"mapbox ([0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?)", result.stdout.strip())
    if result.returncode != 0 or not match:
        raise ValueError("Mapbox CLI version is unavailable")
    version = match.group(1)
    for command, required in COMMANDS.items():
        result = subprocess.run(
            [str(executable), *command, "--schema"], env=env, capture_output=True, text=True, timeout=10
        )
        if result.returncode != 0:
            raise ValueError(f"Mapbox schema failed for {' '.join(command)}")
        schema = json.loads(result.stdout)
        if schema["schema_version"] != 1 or schema["cli"]["version"] != version:
            raise ValueError("Mapbox schema version changed")
        globals = {option["flag"]: option for option in schema["global_options"]}
        if not {"--profile", "--use-login", "--output", "--timeout"}.issubset(globals):
            raise ValueError("required Mapbox global options are missing")
        if "json" not in globals["--output"]["values"]:
            raise ValueError("Mapbox JSON output is unavailable")
        operation = next(item for item in schema["commands"] if item["command"] == "mapbox " + " ".join(command))
        arguments = {argument["flag"]: argument for argument in operation["arguments"]}
        if not required.issubset(arguments):
            raise ValueError(f"required Mapbox options are missing for {' '.join(command)}")
        if command == ("accounts", "list-tokens") and "pk" not in arguments["--usage"]["values"]:
            raise ValueError("Mapbox public-token filter is unavailable")
    print(f"Verified Mapbox CLI {version}: {len(COMMANDS)} auth/token/usage command schemas (no account requests).")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Check an optional Mapbox CLI without credentials")
    parser.add_argument("executable", type=Path, nargs="?")
    args = parser.parse_args()
    try:
        executable = args.executable or os.environ.get("MAPBOX_TUI_CLI") or shutil.which("mapbox")
        if not executable:
            raise ValueError("install Mapbox CLI from https://github.com/mapbox/mapbox-cli#install-a-released-binary")
        check(Path(executable).resolve())
    except (OSError, ValueError, KeyError, StopIteration, subprocess.TimeoutExpired) as error:
        print(f"Mapbox contract check failed: {error}", file=sys.stderr)
        sys.exit(1)
