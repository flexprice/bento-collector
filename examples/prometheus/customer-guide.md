# Daily totals and dated snapshots

Companion example for the customer's PromQL guide. The generic counter example
is unchanged: these are gauges/latest values, not `increase()` counters.

| Query family | Handling |
| --- | --- |
| Reserved CPU / memory | Evaluate `[1d]` at UTC midnight; preserve `instance`. |
| Processed GB / Spark job counts | Preserve `last_over_time` semantics; tenant-level totals. |
| Spark data volume | Filter one `usage_date`; timestamp at that day's next midnight. |
| Weekly / fixed `@` ranges | Supported by Prometheus; reconcile separately, not alongside overlapping daily imports. |

All queries are tenant-filtered. Supply the explicit tenant → Flexprice customer
mapping because `sum by(instance)` and `sum(...)` remove the tenant label.
No instance dimension can be recovered after a tenant-wide `sum(...)`.

## Run

Build the collector and set the connection variables from the [main example](README.md).

```bash
python3 examples/prometheus/customer_queries.py \
  --metric cpu_hours --tenant YOUR_TENANT --customer YOUR_CUSTOMER --day 2026-11-03
# Other variants: memory_gb_hours, data_processed_gb, data_read_gb, data_write_gb,
# spark_job_count / success_count / failed_count / cancelled_count (spark_job_ prefix),
# spark_data_written_gb, spark_data_read_gb.
```

Omit `--day` for yesterday. Add `--continuous` for daily 00:15 UTC polling;
this is an example schedule, **not a confirmed data-finalization time**.
Run one collector per tenant/metric mapping. Use SUM of `properties.value`.
Set a stable, dataset-specific `PROMETHEUS_SOURCE`; use a distinct event name
for each metric. `--print-query` prints the PromQL without starting ingestion.

The ID includes tenant/customer, metric, query, series labels, and UTC day.
Event timestamps are always that day's end, never the current ingestion time.
Spark snapshot retrieval time is deliberately excluded from the identity.
Replaying preserves the exact ID/timestamp pair, even when a later snapshot
changes its value: **dedupe is not a correction workflow**.

## Confirm before billing

- `/60` assumes one recording-rule sample per minute; `/1e9` means decimal GB.
- The global `count_over_time(...)/1440` check does not prove every tenant/pod
  series is complete. The example does not automatically gate on that check.
- Confirm processed-GB/job series reset or represent finalized run/day totals.
  `last_over_time` of a lifetime cumulative value is not a daily delta.
- Spark's seven-day snapshot availability needs timely ingestion, a finalized-day
  policy, durable checkpoints/backfill, and an explicit corrections process.
- `@` fixes the selected window, not the API response timestamp. Partially missing
  history can still return a plausible total; an empty result is not proof of zero.
- Keep rounded daily totals, overlapping windows, missing days, and billing-period
  boundaries in mind. Query success/raw ingestion does not validate invoices.

Prefer direct usage events where exact per-operation billing is required.
The supplied guide says its queries were not run on staging; local reproduction
does not establish parity with the customer's DP metrics service.

## Reproduce

```bash
python3 examples/prometheus/simulate_customer.py
# Explicit opt-in to isolated cloud writes; no billing catalog is created:
python3 examples/prometheus/simulate_customer.py --cloud
./bento-flexprice test examples/prometheus/customer_test.yml
```

The helper imports seven days of minute samples into pinned real Prometheus,
checks every metric variant, daily replay pairs, and anchored weekly totals.
It also exposes missing-series coverage, sampling-cadence errors, cumulative
latest-value ambiguity, and revised snapshots. Logs/JSON stay in ignored
`.simulation-artifacts/`; cloud test events remain in the chosen environment.
