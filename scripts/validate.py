from pathlib import Path
import json
import math
import os
import selectors
import subprocess
import struct
import tempfile

ROOT = Path(__file__).resolve().parents[1]
ENV = {**os.environ, "MAPBOX_ACCESS_TOKEN": ""}
BINS = [ROOT / "bin/shoe"]


def cli(binary, mode, path, *options, success=True):
    p = subprocess.run([str(binary), mode, *([] if path is None else [str(path)]), *options], cwd=ROOT, env=ENV, capture_output=True, text=True, timeout=20)
    assert (p.returncode == 0) == success, (binary.name, p.stderr)
    return json.loads(p.stdout) if success else None


def equivalent(a, b):
    if isinstance(a, float) or isinstance(b, float):
        assert math.isclose(a, b, abs_tol=1e-6, rel_tol=1e-10), (a, b)
    elif isinstance(a, dict):
        assert a.keys() == b.keys(), (a.keys(), b.keys())
        for key in a:
            equivalent(a[key], b[key])
    elif isinstance(a, list):
        assert len(a) == len(b)
        for x, y in zip(a, b):
            equivalent(x, y)
    else:
        assert a == b, (a, b)


class MCP:
    def __init__(self, binary, env=None):
        self.p = subprocess.Popen([str(binary), "mcp"], cwd=ROOT, env=ENV if env is None else env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
        self.id = 0
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.p.stdout, selectors.EVENT_READ)

    def send(self, method, params, notify=False):
        message = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notify:
            self.id += 1
            message["id"] = self.id
        self.p.stdin.write(json.dumps(message) + "\n")
        self.p.stdin.flush()
        if notify:
            return
        assert self.selector.select(15), f"MCP timeout for {method}"
        line = self.p.stdout.readline()
        assert line, f"MCP exited during {method}: {self.p.poll()}"
        result = json.loads(line)
        assert result["id"] == self.id, result
        return result

    def call(self, name, args):
        return self.send("tools/call", {"name": name, "arguments": args})

    def close(self):
        self.p.stdin.close()
        self.p.wait(timeout=10)
        diagnostics = self.p.stderr.read()
        assert self.p.returncode == 0, diagnostics
        self.selector.close()


def validate_mcp(binary):
    m = MCP(binary)
    try:
        init = m.send("initialize", {"protocolVersion": "2025-11-25", "clientInfo": {"name": "prototype-validation", "version": "1"}, "capabilities": {}})
        assert "tools" in init["result"]["capabilities"]
        m.send("notifications/initialized", {}, notify=True)
        tools = m.send("tools/list", {})["result"]["tools"]
        expected = json.loads((ROOT / "shared/tools.json").read_text())
        assert {t["name"]: t["inputSchema"] for t in tools} == {t["name"]: t["inputSchema"] for t in expected}
        assert not m.send("ping", {}).get("error")
        loaded = m.call("load_track", {"path": str(ROOT / "fixtures/example.gpx")})["result"]["structuredContent"]
        track_id = loaded["track_id"]
        summary = m.call("summarize_track", {"track_id": track_id})["result"]["structuredContent"]
        equivalent(summary, loaded["summary"])
        equivalent(summary, cli(binary, "summary", ROOT / "fixtures/example.gpx"))
        for args in ({"point_index": 6}, {"distance_m": summary["distance_m"] / 2}):
            point = m.call("inspect_track", {"track_id": track_id, **args})["result"]["structuredContent"]
            assert point["point"]["segment_index"] >= 0
            options = [item for key, value in args.items() for item in ("--" + key.replace("_", "-"), str(value))]
            equivalent(point, cli(binary, "inspect", ROOT / "fixtures/example.gpx", *options))
        for args in ({"width": 120, "height": 40}, {"width": 160, "height": 50, "bearing": 40, "ascii": True}, {"provider": "mapbox"}):
            result = m.call("render_map", {"track_id": track_id, **args})["result"]["structuredContent"]
            assert "\x1b" not in result["text"]
            assert all(ord(c) >= 32 or c == "\n" for c in result["text"])
            assert len(result["text"].splitlines()) == result["scene"]["height"]
            options = []
            for key, value in args.items():
                flag = "--" + key.replace("_", "-")
                options.extend([flag] if value is True else [flag, str(value)])
            equivalent(result, cli(binary, "render", ROOT / "fixtures/example.gpx", "--json", *options))
            if args.get("provider") == "mapbox":
                assert result["scene"]["tile_stats"]["Requests"] == 0
                assert "missing" in result["scene"]["status"]
        for name, args in [
            ("inspect_track", {"track_id": track_id, "point_index": 999}),
            ("inspect_track", {"track_id": track_id, "point_index": 0, "distance_m": 0}),
            ("summarize_track", {"track_id": "unknown"}),
            ("render_map", {"track_id": track_id, "width": 999999}),
            ("render_map", {"track_id": track_id, "center_lon": 0}),
            ("render_map", {"track_id": "unknown"}),
            ("render_map", {"point_index": 0}),
            ("render_map", {"provider": "demo"}),
            ("load_track", {"path": str(ROOT / "fixtures/invalid.gpx")}),
        ]:
            result = m.call(name, args)
            assert result.get("error") or result.get("result", {}).get("isError"), (name, result)
        for args in ({}, {"center_lon": -0.1, "center_lat": 51.5, "zoom": 13, "ascii": True}, {"provider": "none"}, {"provider": "mapbox"}):
            result = m.call("render_map", args)["result"]["structuredContent"]
            options = ["--json"]
            for key, value in args.items():
                flag = "--" + key.replace("_", "-")
                options.extend([flag] if value is True else [flag, str(value)])
            equivalent(result, cli(binary, "render", None, *options))
            assert result["scene"]["selected_point_index"] == -1
            assert "\x1b" not in result["text"]
            assert not result["text"].strip() and "MAPBOX_ACCESS_TOKEN" in result["scene"]["status"]
        for mode in ("summary", "inspect", "bench"):
            cli(binary, mode, None, success=False)
        cli(binary, "render", None, "--point-index", "0", success=False)
        cli(binary, "render", None, "--center-lon", "0", success=False)
        return summary
    finally:
        m.close()


def validate_cli(binary):
    def run(*argv, success=True):
        result = subprocess.run([str(binary), *argv], cwd=ROOT, env=ENV, capture_output=True, text=True, timeout=20)
        assert (result.returncode == 0) == success, (argv, result.stderr)
        return result

    for argv in (("--help",), ("-h",), *((name, "--help") for name in ("open", "render", "summary", "inspect", "bench", "mcp"))):
        result = run(*argv)
        assert "Usage: shoe" in result.stdout and result.stderr == "", (argv, result)

    path = str(ROOT / "fixtures/example.gpx")
    expected = cli(binary, "render", path, "--json", "--center-lon", "-0.1", "--center-lat", "51.5", "--point-index", "3")
    actual = run("render", "--json", "--center-lon", "-0.1", path, "--point-index", "3", "--center-lat=51.5")
    equivalent(expected, json.loads(actual.stdout))
    actual = run("inspect", "--point-index", "3", path)
    equivalent(cli(binary, "inspect", path, "--point-index", "3"), json.loads(actual.stdout))
    bench = json.loads(run("bench", "--frames", "2", path, "--width", "80", "--height", "20").stdout)
    assert bench["frames"] == len(bench["frame_ms"]) == 2 and bench["point_count"] == 12
    for argv in (("--json",), ("--demo",), ("mcp", path), ("render", "--no-animation")):
        result = run(*argv, success=False)
        assert result.stdout == "" and result.stderr.strip()
    plain = run("render", path)
    assert "No basemap" in plain.stdout and plain.stderr == ""
    requested = run("render", path, "--provider", "mapbox")
    assert "S" in requested.stdout and "warning:" in requested.stderr and "MAPBOX_ACCESS_TOKEN" in requested.stderr
    result = json.loads(run("render", "--json").stdout)
    assert result["scene"]["provider"] == "none" and "MAPBOX_ACCESS_TOKEN" in result["scene"]["status"]


def synthetic_fit(omit_header_crc=False):
    def crc16(data):
        crc = 0
        for byte in data:
            crc ^= byte
            for _ in range(8):
                crc = (crc >> 1) ^ (0xA001 if crc & 1 else 0)
        return crc

    # Build independently of the Go FIT library to check interoperability.
    file_id_definition = bytes.fromhex("400000000001000100")
    record_definition = bytes.fromhex("410000140004fd0486000485010485020284")
    payload = file_id_definition + b"\x00\x04" + record_definition
    for i in range(3):
        payload += struct.pack("<BIiiH", 1, 1_159_776_000 + i * 10, round(51 / 180 * 2**31), round(i * .001 / 180 * 2**31), 2550 + i * 5)
    header = struct.pack("<BBHI4s", 14, 0x20, 2100, len(payload), b".FIT")
    header += struct.pack("<H", 0 if omit_header_crc else crc16(header))
    body = header + payload
    return body + struct.pack("<H", crc16(body))


def validate_imports(binary):
    tcx = b"""<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"><Activities><Activity Sport="Biking"><Lap><Track>
<Trackpoint><Time>2026-10-01T10:00:00Z</Time><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0</LongitudeDegrees></Position><AltitudeMeters>10</AltitudeMeters></Trackpoint>
<Trackpoint><Time>2026-10-01T10:00:10Z</Time><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.001</LongitudeDegrees></Position><AltitudeMeters>11</AltitudeMeters></Trackpoint>
</Track></Lap></Activity></Activities></TrainingCenterDatabase>"""
    kml = b"""<kml xmlns="http://earth.google.com/kml/2.1"><Folder><Placemark><Point><coordinates>0, 51, 10</coordinates></Point><LineString><coordinates>0,51, 10 0.001 , 51 ,11</coordinates></LineString></Placemark></Folder></kml>"""
    m = MCP(binary)
    try:
        m.send("initialize", {"protocolVersion": "2025-11-25", "clientInfo": {"name": "import-validation", "version": "1"}, "capabilities": {}})
        m.send("notifications/initialized", {}, notify=True)
        with tempfile.TemporaryDirectory() as folder:
            for name, body, count, times in (("ride.FIT", synthetic_fit(), 3, 3), ("zwift.fit", synthetic_fit(omit_header_crc=True), 3, 3), ("ride.xml", tcx, 2, 2), ("map.data", kml, 2, 0)):
                path = Path(folder) / name
                path.write_bytes(body)
                summary = cli(binary, "summary", path)
                assert summary["point_count"] == summary["elevation_point_count"] == count
                assert summary["time_point_count"] == times and summary["distance_m"] > 0
                assert summary["elevation_min_m"] == 10 and summary["elevation_max_m"] == 10 + count - 1
                loaded = m.call("load_track", {"path": str(path)})["result"]["structuredContent"]
                equivalent(summary, loaded["summary"])
                track_id = loaded["track_id"]
                equivalent(summary, m.call("summarize_track", {"track_id": track_id})["result"]["structuredContent"])
                point = cli(binary, "inspect", path, "--point-index", "0")
                assert abs(point["point"]["lat"] - 51) < 1e-6 and point["point"]["lon"] == 0
                equivalent(point, m.call("inspect_track", {"track_id": track_id, "point_index": 0})["result"]["structuredContent"])
                rendered = cli(binary, "render", path, "--json", "--provider", "none")
                equivalent(rendered, m.call("render_map", {"track_id": track_id, "provider": "none"})["result"]["structuredContent"])
                assert rendered["scene"]["status"] == "" and rendered["text"].strip()
                invalid = body[:-1] + bytes([body[-1] ^ 1]) if path.suffix.lower() == ".fit" else body[:-1]
                path.write_bytes(invalid)
                cli(binary, "summary", path, success=False)
                assert m.call("load_track", {"path": str(path)})["result"]["isError"]
    finally:
        m.close()


def main():
    for binary in BINS:
        validate_cli(binary)
        validate_mcp(binary)
        validate_imports(binary)
        result=cli(binary,"render",ROOT / "fixtures/example.gpx","--json")
        assert result["scene"]["provider"]=="none" and result["scene"]["status"]==""
        for flag in ("--zoom","--bearing","--cell-aspect"):
            cli(binary,"render",ROOT / "fixtures/example.gpx",flag,"NaN",success=False)
    for name in ("example.gpx", "waypoints.gpx", "dense-10000.gpx", "dense-100000.gpx"):
        path = ROOT / "fixtures" / name
        if not path.exists():
            continue
        for binary in BINS:
            summary = cli(binary, "summary", path)
            assert summary["bounds"] is not None
            for width, height in ((120, 40), (160, 50)):
                result = cli(binary, "render", path, "--json", "--width", str(width), "--height", str(height))
                assert result["scene"]["width"] == width
                assert result["scene"]["height"] == height
                lines = result["text"].splitlines()
                assert len(lines) == height
                assert all(len(line) == width for line in lines)
                assert "\x1b" not in result["text"]
    invalid = ["<gpx><trk>", '<gpx><wpt lat="NaN" lon="0"/></gpx>', '<gpx><wpt lat="51oops" lon="0"/></gpx>', '<gpx><rte><rtept lat="0" lon="179"/><rtept lat="0" lon="-179"/></rte></gpx>']
    with tempfile.TemporaryDirectory() as folder:
        path = Path(folder) / "bad.gpx"
        for body in invalid:
            path.write_text(body)
            for binary in BINS:
                cli(binary, "summary", path, success=False)
    print("Validated CLI dispatch, flag ordering, scoped help, benchmarks, MCP consistency and tool schemas, GPX/FIT/TCX/KML imports and errors, summaries, map scenes and track-free rendering.")


if __name__ == "__main__":
    main()
