#!/usr/bin/env python3
"""Exercise the shipped config against real Prometheus; cloud writes are opt-in."""

import argparse
import datetime as dt
import http.server
import json
import math
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[2]
IMAGE = "prom/prometheus:v3.5.1"


def event_ids(events):
    return {event.get("event_id") or event.get("id") for event in events}


def event_keys(events):
    return {(event.get("event_id") or event.get("id"), event["timestamp"]) for event in events}


def request(url, payload=None, key=None):
    headers = {"Content-Type": "application/json"}
    if key:
        headers["x-api-key"] = key
    data = None if payload is None else json.dumps(payload).encode()
    with urllib.request.urlopen(
        urllib.request.Request(url, data=data, headers=headers), timeout=30
    ) as response:
        return json.load(response)


def wait_for(check, seconds, description):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
        except (urllib.error.URLError, ConnectionError):
            pass
        threading.Event().wait(0.5)
    raise RuntimeError("Timed out: " + description)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cloud", action="store_true", help="Write isolated usage to Flexprice")
    parser.add_argument("--window-seconds", type=int, default=10)
    parser.add_argument("--artifacts", type=Path, default=ROOT / ".simulation-artifacts")
    args = parser.parse_args()
    window = args.window_seconds
    if window not in (10, 60):
        parser.error("--window-seconds must be 10 (fast) or 60 (minute cron)")
    if args.cloud and not os.environ.get("FLEXPRICE_API_KEY"):
        parser.error("--cloud requires FLEXPRICE_API_KEY")

    run_id = uuid.uuid4().hex[:12]
    artifacts = args.artifacts.resolve() / run_id
    artifacts.mkdir(parents=True)
    container = "bento-prometheus-simulation-" + run_id
    started = time.time()
    # Three distinct billing series: two customers, with two regions for customer A.
    series = [
        ("sim_" + run_id + "_a", "us-east-1", 0.2),
        ("sim_" + run_id + "_a", "eu-west-1", 0.4),
        ("sim_" + run_id + "_b", "us-east-1", 0.6),
    ]
    received = []
    metrics_started = []

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            if not metrics_started:
                metrics_started.append(time.time())
            lines = ["# TYPE node_cpu_seconds_total counter"]
            for customer, region, rate in series:
                labels = f'customer_id="{customer}",instance="demo:9100",feature="compute",region="{region}",cpu="0"'
                elapsed = time.time() - started
                for mode, slope in [("user", rate / 2), ("system", rate / 2), ("idle", 10)]:
                    lines.append(f'node_cpu_seconds_total{{{labels},mode="{mode}"}} {100 + elapsed * slope}')
            body = ("\n".join(lines) + "\n").encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; version=0.0.4")
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self):
            if self.path not in ("/v1/events", "/v1/events/bulk"):
                self.send_error(404)
                return
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            events = payload["events"] if self.path.endswith("/bulk") else [payload]
            received.extend(events)
            self.send_response(202)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"message":"accepted"}')

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = free_port()
    config = artifacts / "prometheus.yml"
    config.write_text(json.dumps({
        "global": {"scrape_interval": "1s"},
        "scrape_configs": [{"job_name": "simulation", "static_configs": [
            {"targets": [f"127.0.0.1:{server.server_port}"]}
        ]}],
    }))
    env = dict(os.environ,
        PROMETHEUS_URL=f"http://127.0.0.1:{port}",
        PROMETHEUS_SCHEDULE="0 * * * * *" if window == 60 else "*/10 * * * * *",
        PROMETHEUS_WINDOW_SECONDS=str(window),
        PROMETHEUS_QUERY_OFFSET_SECONDS=str(window),
        PROMETHEUS_POLL_COUNT="2",
        PROMETHEUS_QUERY=f'sum(increase(node_cpu_seconds_total{{mode!="idle"}}[{window}s])) by (customer_id, instance, feature, region)',
        PROMETHEUS_EVENT_NAME="prometheus_simulation_" + run_id,
        PROMETHEUS_SOURCE="prometheus_simulation_" + run_id,
    )
    env.pop("PROMETHEUS_QUERY_TIME", None)
    if not args.cloud:
        env.update(FLEXPRICE_API_KEY="local-simulation-only",
                   FLEXPRICE_API_HOST=f"127.0.0.1:{server.server_port}", FLEXPRICE_SCHEME="http")
    base = env.get("FLEXPRICE_SCHEME", "https") + "://" + env.get("FLEXPRICE_API_HOST", "api.cloud.flexprice.io") + "/v1"

    def read_events():
        if not args.cloud:
            return list(received)
        return request(base + "/events/query", {
            "event_name": env["PROMETHEUS_EVENT_NAME"], "page_size": 100,
        }, env["FLEXPRICE_API_KEY"]).get("events", [])

    def collect(name):
        with (artifacts / (name + ".log")).open("w") as log:
            subprocess.run([str(ROOT / "bento-flexprice"), "-c", str(ROOT / "examples/prometheus-to-flexprice.yaml")],
                           env=env, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT,
                           timeout=window * 4 + 45, check=True)

    try:
        subprocess.run(["docker", "run", "--rm", "-d", "--name", container, "--network", "host",
                        "--mount", f"type=bind,src={config},dst=/etc/prometheus/prometheus.yml,readonly",
                        IMAGE, "--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.path=/prometheus",
                        f"--web.listen-address=127.0.0.1:{port}"], check=True, capture_output=True)
        wait_for(lambda: request(env["PROMETHEUS_URL"] + "/api/v1/query?query=up").get("status") == "success",
                 30, "Prometheus startup")
        # Ensure the delayed, aligned query window has a full history of scrapes.
        wait_for(lambda: metrics_started, 15, "first metric scrape")
        ready_at = math.ceil((metrics_started[0] + window) / window) * window + window + 1
        print(f"Warming up real Prometheus; {window}s windows, cloud={args.cloud}", flush=True)
        threading.Event().wait(max(0, ready_at - time.time()))
        collect("scheduled")
        events = wait_for(lambda: (e if len(e := read_events()) == 6 else None), 90, "six stored usage events")
        (artifacts / "events.json").write_text(json.dumps(events, indent=2))
        expected = {(c, r): slope * window for c, r, slope in series}
        ids = event_ids(events)
        keys = event_keys(events)
        assert len(ids) == 6 and None not in ids, "Expected six distinct event IDs"
        ends = sorted({e["properties"]["window_end"] for e in events})
        assert len(ends) == 2, "Expected two query windows"
        assert (dt.datetime.fromisoformat(ends[1].replace("Z", "+00:00")) -
                dt.datetime.fromisoformat(ends[0].replace("Z", "+00:00"))).total_seconds() == window
        for event in events:
            props = event["properties"]
            amount = float(props["value"])
            assert math.isclose(amount, expected[(event["external_customer_id"], props["region"])], rel_tol=0.02), event
            assert props["feature"] == "compute" and str(props["window_seconds"]) == str(window)
            assert event["timestamp"].replace("+00:00", "Z") == props["window_end"]
            assert all(isinstance(v, str) for v in props.values()), "SDK properties must be strings"

        env.update(PROMETHEUS_QUERY_TIME=str(int(dt.datetime.fromisoformat(ends[0].replace("Z", "+00:00")).timestamp())),
                   PROMETHEUS_POLL_COUNT="1", PROMETHEUS_SCHEDULE="1ms")
        collect("replay")
        if args.cloud:
            # This is a raw-row snapshot, not a metering or invoice check.
            threading.Event().wait(15)
            replayed = read_events()
            replay_ok = len(replayed) == 6 and event_keys(replayed) == keys
        else:
            assert len(received) == 9
            replayed = list(received)
            replay_keys = event_keys(replayed[6:])
            replay_ok = len(replay_keys) == 3 and replay_keys.issubset(keys)
        (artifacts / "replayed-events.json").write_text(json.dumps(replayed, indent=2))
        summary = {"passed": replay_ok, "ingestion_passed": True,
                   "cloud": args.cloud, "run_id": run_id,
                   "event_name": env["PROMETHEUS_EVENT_NAME"], "series": len(series),
                   "scheduled_events": len(events), "windows": ends,
                   "window_seconds": window, "replayed_events": 3,
                   "rows_after_replay": len(replayed),
                   "distinct_ids_after_replay": len(event_ids(replayed)),
                   "replay_ids_stable": event_ids(replayed) == ids,
                   "replay_id_timestamp_pairs_stable": event_keys(replayed) == keys,
                   "replay_check": "cloud raw-row count and stable ID/timestamp pairs" if args.cloud else "stable ID/timestamp pairs at mock sink"}
        (artifacts / "summary.json").write_text(json.dumps(summary, indent=2))
        print(json.dumps(summary, indent=2))
        print("Artifacts:", artifacts)
        if not replay_ok:
            raise RuntimeError("Replay check failed; inspect summary and replayed-events.json. "
                               "Stable IDs do not guarantee immediate raw-row deduplication or billing correctness.")
    finally:
        subprocess.run(["docker", "rm", "-f", container], capture_output=True)
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
