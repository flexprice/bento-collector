"""Reproduce the customer guide on a real, backfilled Prometheus in seconds."""

import argparse
import datetime as dt
import http.server
import json
import os
from pathlib import Path
import subprocess
import threading
import time
import urllib.parse
import uuid

from customer_queries import INSTANCE_LABEL, METRICS, ROOT, TENANT_LABEL, query_for
from simulate import IMAGE, event_keys, free_port, request, wait_for


def fixtures(end):
    lines = []

    def sample(metric, labels, value, timestamp):
        labels = ",".join(f"{k}={json.dumps(v)}" for k, v in labels.items())
        lines.append(f"{metric}{{{labels}}} {value} {timestamp}\n")

    start = end - 7 * 86400
    for metric, values in [("dp_dsi_pod_cpu_requests_cores", (2, 1, 4)),
                           ("dp_dsi_pod_memory_requests_bytes", (4e9, 2e9, 8e9))]:
        for tenant in ("tenant_demo", "tenant_other", "tenant_partial"):
            for pod, (instance, value) in enumerate(zip(("dsi_a", "dsi_a", "dsi_b"), values)):
                labels = {TENANT_LABEL: tenant, INSTANCE_LABEL: instance, "pod": str(pod)}
                for minute in range(1, 7 * 1440 + 1):
                    if tenant == "tenant_partial" and minute % 1440 in range(1, 61):
                        continue
                    sample(metric, labels, value, start + minute * 60)
    for minute in range(1, 7 * 1440 + 1):
        sample("dp_pod_reservation_rule_evaluated", {}, 1, start + minute * 60)
    for tick in range(1, 2881):
        sample("dp_dsi_pod_cpu_requests_cores", {TENANT_LABEL: "tenant_30s", INSTANCE_LABEL: "dsi_a"},
               2, end - 86400 + tick * 30)
    for day in range(7):
        timestamp = start + day * 86400 + 3600
        for run, amount in (("a", 10), ("b", 5)):
            labels = {"trn": "tenant_demo", "run": f"day{day}_{run}"}
            for suffix, value in (("", amount), ("_data_read", amount * .6), ("_data_write", amount * .4)):
                sample("dpr_total_data_processed" + suffix + "_gb", labels, value, timestamp)
        sample("dpr_total_data_processed_gb", {"trn": "tenant_cumulative"}, (day + 1) * 10, timestamp)
        for state, count in (("count", 8), ("success_count", 5), ("failed_count", 2), ("cancelled_count", 1)):
            sample("dpr_spark_pod_runs", {"trn": "tenant_demo", "metric_name": "spark_job_" + state}, count, timestamp)
    for days_back, amount in ((1, 200), (2, 100)):
        day = dt.datetime.fromtimestamp(end - days_back * 86400, dt.timezone.utc).date().isoformat()
        for direction in ("bytes_written", "bytes_read"):
            labels = {"trn": "tenant_demo", "metric_name": direction, "usage_date": day}
            sample("dpr_spark_data_volume", labels, amount * 1e9, end + 60)
            sample("dpr_spark_data_volume", labels, (amount + 50) * 1e9, end + 120)
    return "".join(lines) + "# EOF\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cloud", action="store_true", help="Also send isolated test events to Flexprice")
    parser.add_argument("--artifacts", type=Path, default=ROOT / ".simulation-artifacts")
    args = parser.parse_args()
    if args.cloud and not os.environ.get("FLEXPRICE_API_KEY"):
        parser.error("--cloud requires FLEXPRICE_API_KEY")
    run_id = uuid.uuid4().hex[:12]
    artifacts = args.artifacts.resolve() / ("customer_" + run_id)
    data = artifacts / "data"
    data.mkdir(parents=True)
    end = int(time.time()) // 86400 * 86400 - 86400
    day = dt.datetime.fromtimestamp(end - 86400, dt.timezone.utc).date().isoformat()
    (artifacts / "metrics.om").write_text(fixtures(end))
    (artifacts / "prometheus.yml").write_text("scrape_configs: []\n")
    uid = f"{os.getuid()}:{os.getgid()}"
    mount = ["--mount", f"type=bind,src={artifacts},dst=/fixture"]
    received = []

    class Sink(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            received.extend(payload.get("events", [payload]))
            self.send_response(202)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"message":"accepted"}')

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Sink)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = free_port()
    url = f"http://127.0.0.1:{port}"
    container = "prometheus-customer-" + run_id
    query_results = {}
    env = dict(os.environ, PROMETHEUS_URL=url, PROMETHEUS_QUERY_TIME=str(end + 60),
               PROMETHEUS_SOURCE="customer_simulation_" + run_id,
               FLEXPRICE_API_KEY="local-only", FLEXPRICE_API_HOST=f"127.0.0.1:{server.server_port}",
               FLEXPRICE_SCHEME="http")

    def query(expression, at=end):
        result = request(url + "/api/v1/query?" + urllib.parse.urlencode({"query": expression, "time": at}))
        assert result["status"] == "success" and not result.get("warnings"), result
        assert result["data"]["resultType"] == "vector", result
        return result["data"]["result"]

    def collect(metric, name, settings=None, usage_day=None):
        cfg = dict(env, PROMETHEUS_EVENT_NAME="customer_simulation_" + run_id + "_" + metric)
        cfg.update(settings or {})
        with (artifacts / (name + ".log")).open("w") as log:
            subprocess.run(["python3", str(ROOT / "examples/prometheus/customer_queries.py"),
                            "--metric", metric, "--tenant", "tenant_demo", "--customer", "sim_" + run_id,
                            "--day", usage_day or day], env=cfg, stdout=log, stderr=subprocess.STDOUT, timeout=45, check=True)

    try:
        subprocess.run(["docker", "run", "--rm", "--user", uid, *mount,
                        "--entrypoint", "/bin/promtool", IMAGE, "tsdb", "create-blocks-from", "openmetrics",
                        "/fixture/metrics.om", "/fixture/data"], check=True, capture_output=True)
        subprocess.run(["docker", "run", "--rm", "-d", "--user", uid, *mount, "--name", container,
                        "--network", "host", IMAGE, "--config.file=/fixture/prometheus.yml",
                        "--storage.tsdb.path=/fixture/data", "--storage.tsdb.retention.time=30d",
                        f"--web.listen-address=127.0.0.1:{port}"], check=True, capture_output=True)
        wait_for(lambda: request(url + "/api/v1/query?query=1").get("status") == "success", 30, "Prometheus")
        expected = {"cpu_hours": [72, 96], "memory_gb_hours": [144, 192],
                    "data_processed_gb": [15], "data_read_gb": [9], "data_write_gb": [6],
                    "spark_job_count": [8], "spark_job_success_count": [5],
                    "spark_job_failed_count": [2], "spark_job_cancelled_count": [1],
                    "spark_data_written_gb": [200], "spark_data_read_gb": [200]}
        for metric in METRICS:
            expression = query_for(metric, "tenant_demo").replace("__USAGE_DATE__", day)
            rows = query(expression, end + 60 if metric.startswith("spark_data_") else end)
            assert sorted(float(r["value"][1]) for r in rows) == expected[metric], (metric, rows)
            query_results[metric] = rows
            collect(metric, metric)
        assert len(received) == 13
        initial = list(received)
        keys = event_keys(initial)
        assert len(keys) == len(initial)
        for event in initial:
            assert event["timestamp"] == event["properties"]["window_end"] == dt.datetime.fromtimestamp(end, dt.timezone.utc).isoformat().replace("+00:00", "Z")
            assert event["external_customer_id"] == "sim_" + run_id
            assert all(isinstance(v, str) for v in event["properties"].values())
        for metric in METRICS:
            collect(metric, metric + "_replay", {"PROMETHEUS_QUERY_TIME": str(end + 90)})
        assert len(received) == 26 and event_keys(received[13:]) == keys
        collect("spark_data_written_gb", "late_snapshot", {"PROMETHEUS_QUERY_TIME": str(end + 120)})
        late = received[-1]
        original = next(e for e in initial if e["event_name"].endswith("spark_data_written_gb"))
        assert event_keys([late]) == event_keys([original]) and float(late["properties"]["value"]) == 250
        prior_day = dt.date.fromisoformat(day) - dt.timedelta(days=1)
        collect("cpu_hours", "prior_day", usage_day=prior_day.isoformat())
        assert len(received) == 29 and event_keys(received[-2:]).isdisjoint(keys)
        assert all(e["timestamp"] == dt.datetime.fromtimestamp(end - 86400, dt.timezone.utc).isoformat().replace("+00:00", "Z") for e in received[-2:])
        coverage = query("count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440")
        assert float(coverage[0]["value"][1]) == 1
        partial = query(query_for("cpu_hours", "tenant_partial"))
        assert sorted(float(r["value"][1]) for r in partial) == [69, 92]
        cumulative = query(query_for("data_processed_gb", "tenant_cumulative"))
        assert float(cumulative[0]["value"][1]) == 70
        assert float(query(query_for("cpu_hours", "tenant_30s"))[0]["value"][1]) == 96
        assert not query(query_for("cpu_hours", "tenant_missing"))
        daily_graph = request(url + "/api/v1/query_range?" + urllib.parse.urlencode({
            "query": query_for("cpu_hours", "tenant_demo"), "start": end - 6 * 86400,
            "end": end, "step": 86400}))
        assert daily_graph["status"] == "success" and daily_graph["data"]["resultType"] == "matrix"
        for row in daily_graph["data"]["result"]:
            assert len(row["values"]) == 7 and all(float(v[1]) in (72, 96) for v in row["values"])
        spark_query = query_for("spark_data_written_gb", "tenant_demo").replace(', usage_date="__USAGE_DATE__"', "")
        assert sorted(float(r["value"][1]) for r in query(spark_query, end + 60)) == [100, 200]
        multi_day = (f'sum(dpr_spark_data_volume{{trn="tenant_demo",metric_name="bytes_written",'
                     f'usage_date=~"{prior_day}|{day}"}}) / 1000000000')
        assert float(query(multi_day, end + 60)[0]["value"][1]) == 300
        for metric, totals in (("cpu_hours", [504, 672]), ("memory_gb_hours", [1008, 1344])):
            rows = query(query_for(metric, "tenant_demo", "7d", f" @ {end}"), end + 60)
            assert sorted(float(r["value"][1]) for r in rows) == totals
            assert all(r["value"][0] == end + 60 for r in rows)
        assert sorted(float(r["value"][1]) for r in query(query_for("cpu_hours", "tenant_demo", "31d"))) == [504, 672]
        assert not query(query_for("cpu_hours", "tenant_demo"), end - 60 * 86400)
        cloud = None
        if args.cloud:
            cloud_cfg = {"FLEXPRICE_API_KEY": os.environ["FLEXPRICE_API_KEY"],
                         "FLEXPRICE_API_HOST": os.environ.get("FLEXPRICE_API_HOST", "api.cloud.flexprice.io"),
                         "FLEXPRICE_SCHEME": os.environ.get("FLEXPRICE_SCHEME", "https")}
            for metric in METRICS:
                collect(metric, metric + "_cloud", cloud_cfg)
            base = os.environ.get("FLEXPRICE_SCHEME", "https") + "://" + os.environ.get("FLEXPRICE_API_HOST", "api.cloud.flexprice.io") + "/v1"
            stored = []
            for metric in METRICS:
                payload = {"event_name": "customer_simulation_" + run_id + "_" + metric, "page_size": 100}
                rows = wait_for(lambda: (e if len(e := request(base + "/events/query", payload,
                                os.environ["FLEXPRICE_API_KEY"]).get("events", [])) == len(expected[metric]) else None),
                                90, "cloud events")
                stored.extend(rows)
            assert event_keys(stored) == keys
            (artifacts / "cloud-events.json").write_text(json.dumps(stored, indent=2))
            cloud = {"rows": len(stored), "stable_id_timestamp_pairs": True}
        (artifacts / "queries.json").write_text(json.dumps(query_results, indent=2))
        (artifacts / "events.json").write_text(json.dumps(received, indent=2))
        summary = {"passed": True, "day": day, "event_prefix": "customer_simulation_" + run_id,
                   "metric_variants": len(METRICS), "events": len(initial), "replayed_events": 13,
                   "stable_id_timestamp_pairs": True, "late_snapshot_same_key_new_value": True,
                   "different_day_different_id_timestamp_pairs": True,
                   "global_coverage_can_miss_series_gaps": True, "cumulative_last_value_is_not_daily_delta": True,
                   "thirty_second_samples_double_reserved_total": True,
                   "weekly_anchored_queries": True, "daily_graph_and_spark_date_ranges": True,
                   "partial_history_still_returns_totals": True, "cloud": cloud, "staging_verified": False}
        (artifacts / "summary.json").write_text(json.dumps(summary, indent=2))
        print(json.dumps(summary, indent=2))
        print("Artifacts:", artifacts)
    finally:
        subprocess.run(["docker", "rm", "-f", container], capture_output=True)
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
