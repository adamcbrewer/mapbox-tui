from pathlib import Path
import json
import os
import platform
import statistics
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
ENV = {**os.environ, "MAPBOX_ACCESS_TOKEN": ""}


def main():
    results = []
    for name in ("mapbox-tui",):
        binary = ROOT / "bin" / name
        for fixture in ("example.gpx", "dense-10000.gpx", "dense-100000.gpx"):
            for w, h in ((120, 40), (160, 50)):
                runs, startup = [], []
                for _ in range(3):
                    start = time.perf_counter()
                    p = subprocess.run([str(binary), "summary", str(ROOT / "fixtures" / fixture)], capture_output=True, env=ENV, check=True)
                    startup.append((time.perf_counter() - start) * 1000)
                    with tempfile.TemporaryFile(mode="w+") as output, tempfile.TemporaryFile(mode="w+") as diagnostics:
                        p = subprocess.Popen([str(binary), "bench", str(ROOT / "fixtures" / fixture), "--width", str(w), "--height", str(h), "--frames", "200"], stdout=output, stderr=diagnostics, env=ENV)
                        _, status, usage = os.wait4(p.pid, 0)
                        p.returncode = os.waitstatus_to_exitcode(status)
                        diagnostics.seek(0)
                        assert p.returncode == 0, diagnostics.read()
                        output.seek(0)
                        result = json.load(output)
                        result["max_rss_kib"] = usage.ru_maxrss
                    runs.append(result)
                samples = sorted(x for r in runs for x in r["frame_ms"][1:])
                results.append({"binary": name, "fixture": fixture, "width": w, "height": h, "startup_summary_ms": statistics.median(startup), "load_ms": statistics.median(r["load_ms"] for r in runs), "frame_median_ms": statistics.median(samples), "frame_p95_ms": samples[int(len(samples) * .95)], "max_rss_kib": statistics.median(r["max_rss_kib"] for r in runs)})
    cpu = next((line.split(":", 1)[1].strip() for line in Path("/proc/cpuinfo").read_text().splitlines() if line.startswith("model name")), "unknown")
    metadata = {"platform": platform.platform(), "cpu": cpu, "logical_cpus": os.cpu_count(), "terminal": "headless output; TUI separately exercised under tmux/PTYS", "method": "3 runs × 200 frames; warm synthetic provider, screen thinning + rasterization + plain text; first frame excluded; summary process startup measured separately; os.wait4 max RSS; optimized binaries", "network": "measured separately if local token is available", "results": results}
    folder = ROOT / "reports"
    folder.mkdir(exist_ok=True)
    (folder / "benchmark.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print("| Stack | Points | Cells | Parse ms | Startup ms | Frame median / p95 ms | RSS MiB |")
    print("|---|---:|---|---:|---:|---:|---:|")
    for r in results:
        points = {"example.gpx": 12, "dense-10000.gpx": 10000, "dense-100000.gpx": 100000}[r["fixture"]]
        print(f'| {r["binary"]} | {points:,} | {r["width"]}×{r["height"]} | {r["load_ms"]:.2f} | {r["startup_summary_ms"]:.2f} | {r["frame_median_ms"]:.2f} / {r["frame_p95_ms"]:.2f} | {r["max_rss_kib"]/1024:.1f} |')


if __name__ == "__main__":
    main()
