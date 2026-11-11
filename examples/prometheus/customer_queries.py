"""Run the customer's daily PromQL shapes through the collector."""

import argparse
import datetime as dt
import json
import os
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
TENANT_LABEL = "label_tags_syncron_com_sy_tenant"
INSTANCE_LABEL = "label_tags_syncron_com_dataops_data_service_instance"


def query_for(metric, tenant, period="1d", anchor=""):
    tenant = json.dumps(tenant)
    if metric in ("cpu_hours", "memory_gb_hours"):
        name = "dp_dsi_pod_cpu_requests_cores" if metric == "cpu_hours" else "dp_dsi_pod_memory_requests_bytes"
        divisor = "60" if metric == "cpu_hours" else "60 / 1000000000"
        return (f'sort_desc(round(sum by (instance) (label_replace(sum_over_time('
                f'{name}{{{TENANT_LABEL}={tenant}}}[{period}]{anchor}), '
                f'"instance", "$1", "{INSTANCE_LABEL}", "(.*)")) / {divisor}, 0.01))')
    if metric.startswith("spark_data_"):
        direction = "bytes_written" if metric == "spark_data_written_gb" else "bytes_read"
        return (f'sum by (usage_date) (dpr_spark_data_volume{{trn={tenant}, '
                f'metric_name="{direction}", usage_date="__USAGE_DATE__"}}) / 1000000000')
    if metric.startswith("spark_job_"):
        return f'sum(last_over_time(dpr_spark_pod_runs{{trn={tenant}, metric_name="{metric}"}}[{period}]))'
    name = {"data_processed_gb": "dpr_total_data_processed_gb",
            "data_read_gb": "dpr_total_data_processed_data_read_gb",
            "data_write_gb": "dpr_total_data_processed_data_write_gb"}[metric]
    return f'sum(last_over_time({name}{{trn={tenant}}}[{period}]))'


METRICS = ("cpu_hours", "memory_gb_hours", "data_processed_gb", "data_read_gb", "data_write_gb",
           "spark_job_count", "spark_job_success_count", "spark_job_failed_count",
           "spark_job_cancelled_count", "spark_data_written_gb", "spark_data_read_gb")


def metric_settings(metric):
    return {
        "PROMETHEUS_METRIC_MODE": "usage_date" if metric.startswith("spark_data_") else "window",
        "PROMETHEUS_METRIC_UNIT": "core_hours" if metric == "cpu_hours" else
            "GB_hours" if metric == "memory_gb_hours" else "jobs" if metric.startswith("spark_job_") else "GB",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--metric", choices=METRICS, required=True)
    parser.add_argument("--tenant", required=True, help="Prometheus tenant label value")
    parser.add_argument("--customer", required=True, help="Mapped Flexprice external_customer_id")
    parser.add_argument("--day", type=dt.date.fromisoformat, help="UTC usage day; defaults to yesterday")
    parser.add_argument("--continuous", action="store_true", help="Run daily at 00:15 UTC instead of once")
    parser.add_argument("--print-query", action="store_true")
    args = parser.parse_args()
    if not args.tenant.strip() or not args.customer.strip():
        parser.error("--tenant and --customer must be nonempty")
    if args.continuous and args.day:
        parser.error("--day is a fixed replay; do not combine it with --continuous")
    if args.day and args.day >= dt.datetime.now(dt.timezone.utc).date():
        parser.error("--day must be a completed UTC day")
    query = query_for(args.metric, args.tenant)
    if args.print_query:
        print(query)
        return
    env = dict(os.environ, **metric_settings(args.metric), TZ="UTC",
               PROMETHEUS_QUERY=query, PROMETHEUS_TENANT_ID=args.tenant,
               FLEXPRICE_CUSTOMER_ID=args.customer,
               PROMETHEUS_DAY=args.day.isoformat() if args.day else "",
               PROMETHEUS_POLL_COUNT="0" if args.continuous else "1",
               PROMETHEUS_SCHEDULE="0 15 0 * * *" if args.continuous else "1ms")
    env.setdefault("PROMETHEUS_EVENT_NAME", "prometheus_" + args.metric)
    binary = str(ROOT / "bento-flexprice")
    os.execve(binary, [binary, "-c", str(ROOT / "examples/prometheus/customer-daily.yaml")], env)


if __name__ == "__main__":
    main()
