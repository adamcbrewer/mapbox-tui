from pathlib import Path
import argparse
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description="Check Go formatting without changing files.")
    parser.add_argument("--staged", action="store_true", help="Check staged Go files instead of the working tree.")
    args = parser.parse_args()
    if args.staged:
        names = subprocess.check_output(
            ["git", "diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z", "--", "*.go"], cwd=ROOT
        ).split(b"\0")
        unformatted = []
        for name in filter(None, names):
            path = name.decode(sys.getfilesystemencoding(), "surrogateescape")
            source = subprocess.check_output(["git", "show", f":{path}"], cwd=ROOT)
            result = subprocess.run(["gofmt"], input=source, capture_output=True)
            if result.returncode:
                print(f"{path}: {result.stderr.decode(errors='replace')}", file=sys.stderr)
                return result.returncode
            if result.stdout != source:
                unformatted.append(path)
    else:
        result = subprocess.run(["gofmt", "-l", "."], cwd=ROOT, capture_output=True, text=True)
        if result.returncode:
            print(result.stderr, file=sys.stderr, end="")
            return result.returncode
        unformatted = result.stdout.splitlines()
    if unformatted:
        print("Go formatting differs; run gofmt -w on these files and restage if needed:", file=sys.stderr)
        print("\n".join(unformatted), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
