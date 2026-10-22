import argparse
from http.server import BaseHTTPRequestHandler, HTTPServer
import os
from pathlib import Path
import signal
import tempfile
import threading
import time
from urllib.request import urlopen

from container import ContainerRun
from run import HERE, REPORTS, Stop, digest, now, write_json
from sandbox import preflight


class Endpoint(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"mapbox-dummy-endpoint")

    def log_message(self, *args):
        pass


def boundary_probe(run, checkout):
    script = (HERE / "fixtures/sandbox-probe.sh").read_bytes()
    (checkout / "probe.sh").write_bytes(script)
    run.result["probe_script_sha256"] = digest(script)
    with tempfile.NamedTemporaryFile(prefix="mapbox-host-only-") as host:
        host.write(b"mapbox-dummy-host-file")
        host.flush()
        server = HTTPServer(("127.0.0.1", 0), Endpoint)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            endpoint = f"http://127.0.0.1:{server.server_port}/"
            with urlopen(endpoint, timeout=2) as response:
                if response.read() != b"mapbox-dummy-endpoint":
                    raise Stop("blocked", "Host endpoint control did not respond correctly")
            if Path(host.name).read_bytes() != b"mapbox-dummy-host-file":
                raise Stop("blocked", "Host file control was unavailable")
            run.event("host_controls_verified", host_file=host.name, endpoint=endpoint)
            check, output = run.probe("boundary-probe", checkout,
                                      [host.name, endpoint, os.readlink("/proc/self/ns/net")])
            if check["exit_code"] != 0 or not output.endswith(b"boundary-probes-passed\n"):
                raise Stop("blocked", "A live boundary probe failed; inspect boundary-probe.stdout")
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
    run.result.update(outcome="boundary_passed", reason="Dummy access denials, read-only mounts and kernel resource settings matched the policy.",
                      boundary_probes_passed=True)


def main():
    parser = argparse.ArgumentParser(description="Demonstrate the container boundary using trusted dummy probes.")
    parser.add_argument("--image", required=True)
    parser.add_argument("--phase", choices=("boundary", "timeout", "cancel"), default="boundary")
    args = parser.parse_args()
    run = ContainerRun(REPORTS / "sandbox-probes", args.image)
    run.result.update(workflow="sandbox-probes", phase=args.phase, candidate_executed=False,
                      isolation_demonstrated=False, probe_confirmed=False,
                      probes_sha256=digest(Path(__file__).read_bytes()))
    previous_handlers = {}
    run.event("run_started", phase=args.phase)
    print(run.path, flush=True)
    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signum] = signal.signal(signum, run.cancel)
        preflight(run)
        run.result["outcome"] = "incomplete"
        run.prepare()
        with tempfile.TemporaryDirectory(prefix="mapbox-calibration-probe-") as temporary:
            checkout = Path(temporary)
            if args.phase == "boundary":
                boundary_probe(run, checkout)
            else:
                script = b"set -eu\nprintf 'probe-ready\\n'\nexec sleep 60\n"
                (checkout / "probe.sh").write_bytes(script)
                run.result["probe_script_sha256"] = digest(script)
                run.deadline = time.monotonic() + (5 if args.phase == "timeout" else 60)
                run.probe(args.phase + "-probe", checkout)
                raise Stop("blocked", "The deliberate slow probe finished without interruption")
    except Stop as error:
        run.result.update(outcome=error.outcome, reason=error.reason)
    except KeyboardInterrupt:
        run.result.update(outcome="cancelled", reason="Probe runner interrupted")
    except (OSError, ValueError) as error:
        run.result.update(outcome="blocked", reason=f"Could not run or record the probe: {error}")
    finally:
        run.finalizing = True
        try:
            if args.phase != "boundary":
                output = run.path / (args.phase + "-probe.stdout")
                run.result["probe_confirmed"] = (
                    run.result["outcome"] == ("timeout" if args.phase == "timeout" else "cancelled") and
                    output.exists() and b"probe-ready" in output.read_bytes() and
                    bool(run.result["containers"]) and all(item["removed"] for item in run.result["containers"]))
            else:
                run.result["probe_confirmed"] = run.result["outcome"] == "boundary_passed"
                run.result["isolation_demonstrated"] = run.result["probe_confirmed"]
            run.result.update(ended_at=now(), duration_seconds=round(time.monotonic() - run.started, 4))
            write_json(run.path / "result.json", run.result)
            run.event("run_finished", outcome=run.result["outcome"])
        finally:
            for signum, handler in previous_handlers.items():
                signal.signal(signum, handler)
    print(f"{run.result['outcome']}: {run.result.get('reason')}")
    print(run.path / "result.json")
    return 0 if run.result["outcome"] == "boundary_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
