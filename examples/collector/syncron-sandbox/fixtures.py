"""Syncron-shaped Prometheus data for the last 7 UTC days, with known problems.

Tenants (label_tags_syncron_com_sy_tenant / trn):
  trn_tQAAE7UDDQTNFS  acme     2 instances, full data
  trn_globex          globex   1 instance, full data
  trn_initech         initech  one pod reports negative CPU two days ago
  trn_newco           newco    present in Prometheus but not mapped to a customer

The reservation-rule heartbeat is missing 3 hours on the day three days ago,
so that day fails the completeness check.
"""

import datetime as dt
import json
import sys

TENANT = "label_tags_syncron_com_sy_tenant"
INSTANCE = "label_tags_syncron_com_dataops_data_service_instance"

# tenant -> [(instance, pod, cores, memory_bytes)]
PODS = {
    "trn_tQAAE7UDDQTNFS": [("dsi_analytics", "p1", 2, 4e9), ("dsi_analytics", "p2", 1, 2e9), ("dsi_ingest", "p3", 4, 8e9)],
    "trn_globex": [("dsi_main", "p1", 3, 6e9)],
    "trn_initech": [("dsi_main", "p1", 1, 1e9)],
    "trn_newco": [("dsi_trial", "p1", 1, 1e9)],
}
SPARK_JOBS = {"trn_tQAAE7UDDQTNFS": (8, 5, 2, 1), "trn_globex": (3, 3, 0, 0)}


def generate(end):
    """end is today's 00:00 UTC as a unix timestamp."""
    out = []

    def sample(metric, labels, value, ts):
        body = ",".join(f"{k}={json.dumps(v)}" for k, v in labels.items())
        out.append(f"{metric}{{{body}}} {value} {ts}\n")

    start = end - 7 * 86400
    gap_day = end - 3 * 86400
    negative_day = end - 2 * 86400
    for minute in range(0, 7 * 1440):
        ts = start + minute * 60 + 30
        for tenant, pods in PODS.items():
            for instance, pod, cores, mem in pods:
                labels = {TENANT: tenant, INSTANCE: instance, "pod": pod}
                c = cores
                if tenant == "trn_initech" and negative_day <= ts < negative_day + 86400:
                    c = -1
                sample("dp_dsi_pod_cpu_requests_cores", labels, c, ts)
                sample("dp_dsi_pod_memory_requests_bytes", labels, mem, ts)
        if not (gap_day + 3600 <= ts < gap_day + 4 * 3600):
            sample("dp_pod_reservation_rule_evaluated", {}, 1, ts)
    for day in range(7):
        ts = start + day * 86400 + 6 * 3600
        for tenant, counts in SPARK_JOBS.items():
            for name, n in zip(("count", "success_count", "failed_count", "cancelled_count"), counts):
                sample("dpr_spark_pod_runs", {"trn": tenant, "metric_name": "spark_job_" + name}, n, ts)
    out.append("# EOF\n")
    return "".join(out)


if __name__ == "__main__":
    today = int(dt.datetime.now(dt.timezone.utc).timestamp()) // 86400 * 86400
    sys.stdout.write(generate(today))
