import argparse
import json
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time

from case_suite import extract_archive
from run import HERE, REPORTS, ROOT, Run, Stop, apply_patch, digest, now, write_json

sys.path.insert(0, str(ROOT / "scripts"))
from viewer_probe import preflight, probe

BASE = "6f119511d5653f2d296c11b273e5f9032ce5f331"
SEED = HERE / "fixtures/viewer_modal_seed.patch"

def calibrate(run):
    run.result.update(base_commit=BASE, model_calls=0, execution_mode="trusted-fixed-calibration",
                      seed_sha256=digest(SEED.read_bytes()), observations=[])
    run.result["checker_sha256"] = {str(path.relative_to(ROOT)): digest(path.read_bytes()) for path in
        (Path(__file__).resolve(), ROOT / "scripts/interactions.py", ROOT / "scripts/control_interactions.py", ROOT / "scripts/validate.py", HERE / "viewer_probe.py")}
    preflight()
    check, raw = run.command("source", ["git", "archive", BASE], ROOT)
    if check["exit_code"]:
        raise Stop("blocked", "Pinned source unavailable")
    run.result["source_archive_sha256"] = digest(raw)
    with tempfile.TemporaryDirectory(prefix="mb-viewer-source-") as folder:
        checkout = Path(folder)
        extract_archive(raw, checkout)
        original = (checkout / "go/control.go").read_bytes()
        for stage, expected in (("original", "passed"), ("planted", "modal_import_stalled"), ("restored", "passed")):
            if stage == "planted":
                apply_patch(run, checkout, SEED.read_bytes(), "seed", ["go/control.go"])
            if stage == "restored":
                check, _ = run.command("restore", ["git", "apply", "-R", str(SEED)], checkout)
                if check["exit_code"] or (checkout / "go/control.go").read_bytes() != original:
                    raise Stop("blocked", "Reference restoration did not recover the original bytes")
            binary = checkout / "viewer"
            check, _ = run.command(stage + "-build", ["go", "build", "-buildvcs=false", "-o", str(binary), "./go"], checkout)
            if check["exit_code"]:
                raise Stop("blocked", "Could not build " + stage)
            output = run.path / stage
            try:
                check, _ = run.command(stage + "-probe", [sys.executable, str(Path(__file__).resolve()),
                    "--binary", str(binary), "--output", str(output)], ROOT)
            finally:
                owner = output / "owner.json"
                if owner.exists():
                    owned = json.loads(owner.read_text())
                    subprocess.run(["tmux", "-L", owned["socket"], "kill-server"], capture_output=True, timeout=10)
                    if owned.get("runtime_directory") and Path(owned["runtime_directory"]).exists():
                        shutil.rmtree(owned["runtime_directory"])
            result = json.loads((output / "result.json").read_text())
            run.result["observations"].append({"stage": stage, "expected": expected, **result})
            if result["outcome"] in ("blocked", "cancelled"):
                raise Stop(result["outcome"], stage + ": " + result.get("reason", "No check result"))
            if result["outcome"] != expected or not result["cleanup"] or check["exit_code"] != (0 if expected == "passed" else 1):
                raise Stop("calibration_failed", stage + ": " + result.get("reason", "Unexpected outcome"))
    run.result.update(outcome="viewer_calibration_passed", reason="Original passed, planted bug failed, restored code passed. Human review is pending.")


def report(run):
    lines = ["# Viewer modal/import calibration", "",
        "This first slice of #110 checks the checker before connecting a repair model. "
        "Our script builds only fixed, trusted code. Pi and a repair model did not run. "
        "Passing this calibration does not mean a proposed repair or the issue is approved.", "",
        "Input: start an empty 120×40 viewer, open Commands with Ctrl+K, submit a synthetic two-point "
        "import through attached MCP without waiting, then poll its operation for up to ten seconds. "
        "Expected: applied, one document, two visible points, selected imported item, Commands still open.", "",
        "Each viewer uses temporary settings, runtime, home and cache, no Mapbox credentials/provider, "
        "TERM=xterm-256color, COLORTERM=truecolor and NO_COLOR unset. "
        "The script also checks an unavailable viewer, a failed import without document changes, "
        "a stale-revision rejection and cleanup.", ""]
    for item in run.result.get("observations", []):
        stage = item["stage"]
        matched = item["outcome"] == item["expected"] and item["cleanup"]
        label = ("Deliberate bug was detected" if stage == "planted" else "Expected viewer state passed") if matched else "Check needs attention"
        lines += [f"## {stage.capitalize()}: {label}", "", item.get("reason", "No result"), "",
            f"Technical result: `{item['outcome']}`. Cleanup confirmed: {item['cleanup']}.", "",
            f"[Before capture]({stage}/before-import.png) · [After capture]({stage}/after-import.png) · "
            f"[State snapshots]({stage}/states.jsonl) · [Receipt]({stage}/result.json)", ""]
    lines += ["## What this proves and what remains", "",
        run.result.get("reason", "Calibration incomplete."), "",
        "The planted guard drops import completion while a dialog is open. The viewer stays responsive, "
        "but the accepted operation remains pending and no document appears. Restoring the original bytes "
        "makes the same sequence pass. Missing tools, missing viewers and uncertain states are blockers, "
        "not evidence of this regression. The ten-second wait bounds detection; it does not repair product timing.", "",
        "Review the captures for clipping or rendering mistakes. Structured assertions prove state, not visual quality. "
        "No held-out cases, independently checked model patch, container terminal capability or performance benefit "
        "is established by this slice. Human review/attention time and execution billing are unknown. "
        "Review the successful and deliberately failed examples before connecting Pi.", "",
        "Run `python3 tools/harness/viewer_calibration.py` again from the checkout with the project Go toolchain "
        "and terminal prerequisites. Each invocation keeps a new local evidence directory. "
        "Original/restored probes exit 0; the deliberately broken probe exits 1; the combined calibration exits 0 "
        "only when all three expected outcomes and cleanup match.", "",
        "[Run logs and exact commands](result.json) · [Planted patch](seed.patch) · "
        "[Shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111)", ""]
    (run.path / "calibration.md").write_text("\n".join(lines))


def main():
    parser = argparse.ArgumentParser(description="Calibrate the trusted modal/import viewer reproducer without a repair model.")
    parser.add_argument("--binary", type=Path, help="Probe a trusted binary. Never use this host command for model-generated code.")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.binary:
        if not args.output or args.output.exists():
            parser.error("--binary needs a new --output directory")
        def stop(signum, frame):
            raise KeyboardInterrupt
        signal.signal(signal.SIGTERM, stop)
        result = probe(args.binary.resolve(), args.output.resolve())
        print(result["outcome"], result.get("reason", ""))
        return 0 if result["outcome"] == "passed" else 1
    if args.output:
        parser.error("--output requires --binary")
    run = Run(REPORTS / "viewer-calibration")
    handlers = {s: signal.signal(s, run.cancel) for s in (signal.SIGTERM, signal.SIGINT)}
    try:
        calibrate(run)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Calibration interrupted")
    except (Stop, OSError, ValueError, subprocess.SubprocessError) as error:
        run.result.update(outcome=getattr(error, "outcome", "blocked"), reason=getattr(error, "reason", str(error)))
    finally:
        run.finalizing = True
        run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
        write_json(run.path / "result.json", run.result)
        report(run)
        for s, handler in handlers.items():
            signal.signal(s, handler)
    print(run.result["outcome"], run.path / "result.json")
    return 0 if run.result["outcome"] == "viewer_calibration_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
