"""Play Syncron end to end: connect, map, preview, save, run, re-run, backfill.

Needs the sandbox Prometheus (./up.sh), a Flexprice with the setup applied
(flexprice_setup.py) and `bento-flexprice collector serve` running.

  COLLECTOR_URL     default http://127.0.0.1:8090
  COLLECTOR_UI_TOKEN
  COLLECTOR_DATA_DIR / COLLECTOR_CONNECTIONS_FILE, PROMETHEUS_URL, PROMETHEUS_PASSWORD
                    same as the serve process, for CLI runs
  BENTO_FLEXPRICE   path to the binary
  FLEXPRICE_URL, FLEXPRICE_API_KEY, FLEXPRICE_ENV_ID
  PROM_URL, PROM_USER, PROM_PASSWORD
"""

import datetime as dt
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

API = os.environ.get("COLLECTOR_URL", "http://127.0.0.1:8090")
TOKEN = os.environ.get("COLLECTOR_UI_TOKEN", "")
BIN = os.environ.get("BENTO_FLEXPRICE", "bento-flexprice")
FP = os.environ.get("FLEXPRICE_URL", "http://localhost:8080")
FP_KEY = os.environ["FLEXPRICE_API_KEY"]
FP_ENV = os.environ.get("FLEXPRICE_ENV_ID", "")
PROM = os.environ.get("PROM_URL", "http://127.0.0.1:9095")
PROM_PASSWORD = os.environ.get("PROMETHEUS_PASSWORD", os.environ.get("PROM_PASSWORD", "syncron-secret"))

today = dt.datetime.now(dt.timezone.utc).date()
D = lambda n: (today - dt.timedelta(days=n)).isoformat()  # noqa: E731
results = []


def call(method, url, body=None, headers=None):
    req = urllib.request.Request(url, method=method, headers={"Content-Type": "application/json", **(headers or {})},
                                 data=json.dumps(body).encode() if body is not None else None)
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except json.JSONDecodeError:
            return e.code, {}


def api(method, path, body=None):
    return call(method, API + path, body, {"Authorization": "Bearer " + TOKEN})


def fp(method, path, body=None):
    h = {"x-api-key": FP_KEY}
    if FP_ENV:
        h["x-environment-id"] = FP_ENV
    return call(method, FP + path, body, h)


def record(name, ok, detail):
    results.append({"scenario": name, "pass": bool(ok), "detail": detail})
    print(("PASS " if ok else "FAIL ") + name + " — " + detail, flush=True)


def cli(*args, env=None):
    p = subprocess.run([BIN, "collector", "run", *args], capture_output=True, text=True, timeout=300, env={**os.environ, **(env or {})})
    runs = [json.loads(line) for line in p.stdout.splitlines() if line.startswith("{")]
    return p.returncode, runs, p.stderr


def first_error(runs):
    return next((c["message"] for r in runs for c in r["checks"] if c["level"] == "error"), "")


def settled_usage(event_name, customer, day, expect, seconds=60):
    """Flexprice ingests asynchronously (Kafka -> ClickHouse); poll until the value settles."""
    deadline, value = time.time() + seconds, None
    while time.time() < deadline:
        value = usage(event_name, customer, day)
        if abs(value - expect) < 1e-6:
            time.sleep(5)  # stay a moment to catch late duplicates
            return usage(event_name, customer, day)
        time.sleep(2)
    return value


def usage(event_name, customer, day):
    meters = fp("GET", "/v1/meters?limit=500")[1]["items"]
    meter = next(m for m in meters if m["event_name"] == event_name)
    start = dt.datetime.fromisoformat(day + "T00:00:00+00:00")
    _, r = fp("POST", "/v1/events/usage/meter", {"meter_id": meter["id"], "external_customer_id": customer,
                                                 "start_time": start.isoformat(), "end_time": (start + dt.timedelta(days=1)).isoformat()})
    return r.get("value", 0)


CPU_QUERY = ('sum by (label_tags_syncron_com_sy_tenant, instance) (label_replace(sum_over_time('
             'dp_dsi_pod_cpu_requests_cores[1d]), "instance", "$1", '
             '"label_tags_syncron_com_dataops_data_service_instance", "(.*)")) / 60')
LOOKUP = {"trn_tQAAE7UDDQTNFS": "cust_acme", "trn_globex": "cust_globex", "trn_initech": "cust_initech"}


def cpu_collector(**mapping):
    m = {"event_name": "syncron_cpu_hours", "unit": "core_hours", "required_labels": ["instance"],
         "customer": {"from": "label", "label": "label_tags_syncron_com_sy_tenant", "lookup": dict(LOOKUP)}}
    m.update(mapping)
    return {"id": "syncron-cpu-hours", "name": "Syncron reserved CPU core-hours",
            "source": {"type": "prometheus", "connection": "syncron-prom", "query": CPU_QUERY, "mode": "window"},
            "trigger": {"schedule": "15 0 * * *", "window": "1d",
                        "completeness": {"query": "count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440", "min": 0.999}},
            "mapping": m, "destination": "flexprice"}


def main():
    # 1. Connections come from the deployment's connections file
    _, conns = api("GET", "/api/connections")
    record("Connect: connections listed from the connections file", {"syncron-prom", "flexprice"} <= {c["id"] for c in conns}, json.dumps([{k: c.get(k) for k in ("id", "secret_env")} for c in conns]))
    _, t = api("POST", "/api/connections/syncron-prom/test")
    record("Connect: Prometheus with basic auth", t["ok"], t["message"])
    _, t = api("POST", "/api/connections/flexprice/test")
    record("Connect: Flexprice", t["ok"], t["message"])
    record("Connect: secrets never returned by the API", PROM_PASSWORD not in json.dumps(conns) and FP_KEY not in json.dumps(conns), "only secret_env names")
    st, _ = api("PUT", "/api/connections/syncron-prom", {"type": "prometheus", "url": "http://evil.example"})
    record("Connect: the UI cannot change a connection", st >= 400, f"PUT -> {st}")

    # 2. Preview a normal day
    _, p = api("POST", "/api/preview", {"collector": cpu_collector(), "day": D(1)})
    got = sorted((r["event"]["external_customer_id"], r["event"]["properties"]["instance"], float(r["event"]["properties"]["value"])) for r in p["rows"] if not r.get("error"))
    want = [("cust_acme", "dsi_analytics", 72.0), ("cust_acme", "dsi_ingest", 96.0), ("cust_globex", "dsi_main", 72.0), ("cust_initech", "dsi_main", 24.0)]
    rej = [r["error"] for r in p["rows"] if r.get("error")]
    record("Preview: yesterday maps 4 events with exact core-hours", got == want, f"{got}")
    record("Preview: unmapped tenant newco is rejected, not billed", len(rej) == 1 and "trn_newco" in rej[0], rej[0] if rej else "no reject")
    record("Preview: nothing sent to Flexprice", usage("syncron_cpu_hours", "cust_acme", D(1)) == 0, "usage still 0")

    # 3. Product decision: newco is a trial tenant -> skip instead of reject
    _, p = api("POST", "/api/preview", {"collector": cpu_collector(customer={"from": "label", "label": "label_tags_syncron_com_sy_tenant", "lookup": dict(LOOKUP), "skip": ["trn_newco"]}), "day": D(1)})
    record("Preview: trial tenant listed as not billed is skipped", p["skipped"] == 1 and p["rejected"] == 0 and p["events"] == 4, f"events={p['events']} skipped={p['skipped']} rejected={p['rejected']}")

    # 4. Incomplete day
    _, p = api("POST", "/api/preview", {"collector": cpu_collector(), "day": D(3)})
    msg = next((c["message"] for c in p["checks"] if "not fully measured" in c["message"]), "")
    record("Preview: day with a 3h metrics gap is blocked by completeness", bool(msg) and not p["ok"], msg)

    # 5. Bad data
    _, p = api("POST", "/api/preview", {"collector": cpu_collector(customer={"from": "label", "label": "label_tags_syncron_com_sy_tenant", "lookup": dict(LOOKUP), "skip": ["trn_newco"]}), "day": D(2)})
    neg = [r["error"] for r in p["rows"] if r.get("error")]
    record("Preview: negative CPU for initech is rejected", any("Negative value" in e for e in neg), "; ".join(neg))

    # 6. Missing meter / unknown customer
    _, p = api("POST", "/api/preview", {"collector": cpu_collector(event_name="syncron_cpu_hour"), "day": D(1)})
    record("Preview: event name with no meter is blocked", any("No Flexprice meter" in c["message"] for c in p["checks"]), next((c["message"] for c in p["checks"] if c["level"] == "error"), ""))
    lk = dict(LOOKUP, trn_newco="cust_newco")
    _, p = api("POST", "/api/preview", {"collector": cpu_collector(customer={"from": "label", "label": "label_tags_syncron_com_sy_tenant", "lookup": lk}), "day": D(1)})
    unknown = [r["error"] for r in p["rows"] if r.get("error")]
    record("Preview: customer missing in Flexprice is rejected", any("cust_newco" in e and "does not exist" in e for e in unknown), "; ".join(unknown))

    # 7. Save versions
    st, v1 = api("PUT", "/api/collectors/syncron-cpu-hours", cpu_collector())
    st2, v2 = api("PUT", "/api/collectors/syncron-cpu-hours", cpu_collector(customer={"from": "label", "label": "label_tags_syncron_com_sy_tenant", "lookup": dict(LOOKUP), "skip": ["trn_newco"]}))
    _, versions = api("GET", "/api/collectors/syncron-cpu-hours/versions")
    record("Save: each save is a new immutable version", st == st2 == 200 and [v["version"] for v in versions][:2] == [v2["version"], v1["version"]], f"versions={[v['version'] for v in versions]}")
    st, bad = api("PUT", "/api/collectors/syncron-cpu-hours", dict(cpu_collector(), source={"type": "prometheus", "connection": "nope", "query": CPU_QUERY}))
    record("Save: invalid spec is refused", st == 400, bad.get("error", ""))

    # 8. Run yesterday
    code, runs, err = cli("--collector", "syncron-cpu-hours", "--day", D(1))
    r = runs[0] if runs else {}
    acme = settled_usage("syncron_cpu_hours", "cust_acme", D(1), 168)
    record("Run: yesterday sends 4 events, exit 0", code == 0 and r.get("sent") == 4, f"exit={code} sent={r.get('sent')} skipped={r.get('skipped')} {err.strip()[:200]}")
    record("Run: Flexprice usage equals Prometheus (acme 72+96=168)", abs(acme - 168) < 1e-6, f"acme usage={acme}")

    # 9. Re-run same day: no double billing
    code, runs, _ = cli("--collector", "syncron-cpu-hours", "--day", D(1))
    time.sleep(10)
    acme2 = settled_usage("syncron_cpu_hours", "cust_acme", D(1), 168)
    record("Re-run: same day again does not double-bill", code == 0 and abs(acme2 - 168) < 1e-6, f"acme usage after re-run={acme2}")

    # 10. Backfill
    code, runs, _ = cli("--collector", "syncron-cpu-hours", "--from", D(6), "--day", D(2))
    summary = {x["window"]["start"][:10]: ("ok" if x["ok"] else "FAILED") + f" sent={x['sent']} rejected={x['rejected']}" for x in runs}
    gap_failed = summary.get(D(3), "").startswith("FAILED") and " sent=0" in summary.get(D(3), "")
    neg_day = summary.get(D(2), "")
    record("Backfill: 5 days run; gap day sends nothing; negative day partially sends", code == 1 and gap_failed and "rejected=1" in neg_day, json.dumps(summary))

    # 11. Prometheus down, wrong credentials
    code, runs, err = cli("--collector", "syncron-cpu-hours", "--day", D(1), "--dry-run", env={"PROMETHEUS_URL": "http://127.0.0.1:9"})
    record("Outage: Prometheus unreachable fails the run (exit 1)", code == 1 and (not runs or not runs[0]["ok"]), (first_error(runs) or err.strip())[:200])
    code, runs, err = cli("--collector", "syncron-cpu-hours", "--day", D(1), "--dry-run", env={"PROMETHEUS_PASSWORD": "wrong-password"})
    record("Credentials: wrong Prometheus password fails the run", code == 1 and (not runs or not runs[0]["ok"]), (first_error(runs) or err.strip())[:200])
    code, runs, err = cli("--collector", "syncron-cpu-hours", "--day", D(1), "--dry-run", env={"FLEXPRICE_API_KEY": "sk_bad"})
    record("Credentials: bad Flexprice key fails the run", code == 1 and (not runs or not runs[0]["ok"]), (first_error(runs) or err.strip())[:200])

    # 12. Second metric: Spark jobs per tenant
    spark = {"id": "syncron-spark-jobs", "name": "Syncron Spark jobs",
             "source": {"type": "prometheus", "connection": "syncron-prom", "mode": "window",
                        "query": 'sum by (trn) (last_over_time(dpr_spark_pod_runs{metric_name="spark_job_count"}[1d]))'},
             "trigger": {"schedule": "15 0 * * *", "window": "1d"},
             "mapping": {"event_name": "syncron_spark_job_count", "unit": "jobs",
                         "customer": {"from": "label", "label": "trn", "lookup": {"trn_tQAAE7UDDQTNFS": "cust_acme", "trn_globex": "cust_globex"}}},
             "destination": "flexprice"}
    api("PUT", "/api/collectors/syncron-spark-jobs", spark)
    code, runs, _ = cli("--all", "--day", D(1))
    sent = {x["collector"]: x["sent"] for x in runs}
    record("Run --all: every saved collector runs in one job", code == 0 and sent == {"syncron-cpu-hours": 4, "syncron-spark-jobs": 2}, json.dumps(sent))

    passed = sum(r["pass"] for r in results)
    print(f"\n{passed}/{len(results)} scenarios passed")
    out = os.environ.get("SCENARIO_REPORT")
    if out:
        with open(out, "w") as f:
            json.dump({"days": {"yesterday": D(1), "gap": D(3), "negative": D(2)}, "results": results}, f, indent=2)
    sys.exit(0 if passed == len(results) else 1)


if __name__ == "__main__":
    main()
