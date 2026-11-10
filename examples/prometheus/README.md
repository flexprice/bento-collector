# Prometheus → Flexprice

Use Bento's `generate` + `http` to turn customer-labeled counter deltas into
windowed usage events. **For accurate, auditable per-operation billing, prefer
sending usage events directly to Flexprice.** This example is not a durable collector.

## Run

```bash
go build -o bento-flexprice main.go
export PROMETHEUS_URL=http://localhost:9090
export FLEXPRICE_API_HOST=api.cloud.flexprice.io
export FLEXPRICE_API_KEY=your_api_key_here
./bento-flexprice -c examples/prometheus-to-flexprice.yaml
```

`FLEXPRICE_API_HOST` is a hostname, not a URL or `/v1`.
[All settings](../prometheus-to-flexprice.yaml) are environment-configurable.
Defaults: minute cron `0 * * * * *`, 60-second windows, 60-second query delay.

```promql
sum(increase(node_cpu_seconds_total{mode!="idle"}[1m]))
  by (customer_id, instance, feature, region)
```

Ordinary node-exporter metrics lack `customer_id`; supply labeled counters or
trusted enrichment. Preserve pricing dimensions in `by (...)`. This value is
**CPU-seconds, not CPU percentage**: configure a **SUM of `properties.value`**,
not COUNT. Properties preserve labels, value, and window bounds as strings.

## IDs and timestamps

Prometheus supplies labels, an evaluation timestamp, and a value—**no unique
usage-event/response ID**. We call `/api/v1/query` at a fixed window end with
`increase(counter[1m])`. The lookback defines the window; `/query_range` instead
returns multiple evaluation points.

```text
start_epoch = end_epoch - window_seconds
event_id = hash(source, event_name, query, sorted_labels, window_seconds, end_epoch)
timestamp = window_end
```

One ID is generated **per series per window**. Flexprice dedupes on **event ID
+ timestamp**; replay must preserve both. Never use execution time as the event
timestamp or add randomness to the ID. Keep source, query text, and labels stable.

Scheduled ends are aligned: `floor(now / window) * window - offset`.
For five-minute windows, use cron `0 */5 * * * *`, query range `[5m]`,
and window/offset `300`. Set `PROMETHEUS_QUERY_TIME` to a fixed end epoch for replay.

## Billing gotchas

- **Overlap:** 12:05–12:10 and 12:08–12:12 get different IDs but overlap usage.
  Enforce canonical non-overlapping windows, including manual runs.
- **Durability:** add a persistent cursor, downtime backfill, and HA coordination;
  this memory-only example does not provide them.
- **Accuracy:** `increase()` extrapolates samples. Scrape gaps, resets, and late
  samples can lose/revise usage; replay is not a correction workflow.
- **Attribution:** preserve customer/pricing labels and assess their cardinality.
- **Dedupe:** test billed SUM under replay. Raw readback can show unmerged duplicate
  ID/timestamp pairs; raw counts or stable IDs alone do not prove invoice correctness.

Errors, warnings, non-vector results, missing customers, invalid/negative values,
and wrong timestamps are rejected. Empty vectors emit no events, not zeros.
Nack retries can resend partial successes. Monitor errors/empty results and
configure Prometheus auth/TLS. Original operations cannot be reconstructed.

## Verify

Requires Python 3.8+, Docker host networking, and the built collector:

```bash
./bento-flexprice test examples/prometheus/pipeline_test.yml
python3 -m unittest discover -s examples/prometheus -p 'test_*.py'
python3 examples/prometheus/simulate.py
python3 examples/prometheus/simulate.py --window-seconds 60
# Explicit opt-in: writes isolated usage to your Flexprice environment.
python3 examples/prometheus/simulate.py --cloud
```

The helper sends three synthetic series over two windows, then replays one.
Local mode verifies identical ID/timestamp pairs; cloud mode also requires six
raw rows after 15 seconds. Unmerged duplicates fail that strict snapshot check.
This does not test invoices or create customers, meters, plans, or subscriptions.

Logs, events, and replay summaries (including failures) are saved under ignored
`.simulation-artifacts/<run_id>/`. Cloud test usage remains; use a sandbox.
The exporter and Prometheus are cleaned up automatically.

[Prometheus API](https://prometheus.io/docs/prometheus/latest/querying/api/) ·
[increase()](https://prometheus.io/docs/prometheus/latest/querying/functions/#increase)
