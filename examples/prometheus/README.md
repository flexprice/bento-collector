# Prometheus → Flexprice

Flexprice Collector can use Bento's existing `generate` input and `http`
processor to poll Prometheus. No native Prometheus input plugin is required.
The [example config](../prometheus-to-flexprice.yaml) turns each instant-vector
series into one Flexprice event for one customer and one query window.

```text
Application counters → Prometheus scrapes → scheduled PromQL instant query
  → validate response → split series → map customer / timestamp / properties
  → Flexprice event ingestion → your configured usage meter
```

This is a polling integration example, not a durable billing collector. It does
not create customers, meters, plans, subscriptions, prices, or invoices.

## Run the collector

Build from the repository root, as in the main README:

```bash
go build -o bento-flexprice main.go
export PROMETHEUS_URL=http://localhost:9090
export FLEXPRICE_API_HOST=api.cloud.flexprice.io
export FLEXPRICE_API_KEY=your_api_key_here
./bento-flexprice lint examples/prometheus-to-flexprice.yaml
./bento-flexprice -c examples/prometheus-to-flexprice.yaml
```

`api_host` is **only a hostname** (and optionally port). Do not include a scheme
or `/v1`: the output plugin/SDK supplies the API path. The HTTP processor sends
the current message as its request body; it does **not** accept a `payload`
field. The generate mapping builds a URL-encoded form body before the request.

The example assumes this counter has a `customer_id` label:

```promql
sum(increase(node_cpu_seconds_total{mode!="idle"}[1m]))
  by (customer_id, instance, feature, region)
```

Ordinary node-exporter CPU metrics do **not** have customer attribution. The
simulator deliberately adds labels to demonstrate the mapping. Use your own
customer-labeled counter or a trusted instance-to-customer enrichment step;
never silently use an instance name as a customer ID. Keep every dimension you
need to bill on in the `by` clause. Labels removed there cannot be recovered.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PROMETHEUS_URL` | `http://localhost:9090` | Prometheus HTTP API |
| `PROMETHEUS_SCHEDULE` | `0 * * * * *` | Six-field cron, once per minute |
| `PROMETHEUS_QUERY` | Counter query above | Must return an instant vector |
| `PROMETHEUS_WINDOW_SECONDS` | `60` | Alignment and event window metadata |
| `PROMETHEUS_QUERY_OFFSET_SECONDS` | `60` | Delay for completed scrape windows |
| `PROMETHEUS_QUERY_TIME` | Unset | Fixed Unix-seconds evaluation time for replay |
| `PROMETHEUS_POLL_COUNT` | `0` | Unlimited; set a positive count for a bounded run |
| `PROMETHEUS_EVENT_NAME` | `prometheus_cpu_usage` | Flexprice event name |
| `PROMETHEUS_SOURCE` | `prometheus` | Stable source identity; make unique per Prometheus dataset |

**Change the schedule, PromQL range, window seconds, and delay together.** A
10-second schedule with a 60-second query double-counts overlapping usage. The
default evaluates at `floor(now / 60) * 60 - 60`: a delayed, aligned window end,
not a jittered "now". Events use that evaluation timestamp, not ingestion time.
The `increase` value is CPU-seconds across the window, not a CPU percentage.

Each event has `external_customer_id`, an RFC3339 UTC `timestamp`, and string
properties containing the returned labels, `value`, `window_seconds`,
`window_start`, and `window_end`. Configure a **SUM of `properties.value`**, not
COUNT of events, if you want to meter this delta. SUM and customer billing still
require your own correctly configured meter and subscription; this example
does not prove invoice amounts.

The event ID hashes the source, event name, query, window size, evaluation time,
and sorted label set. Retrying the same window yields the same IDs. Changing
source, query text, or labels changes IDs; do not change these while replaying
already billed windows. A later query may also revise the value for an existing
ID due to late data: deduplication is not a correction workflow.

### Failures and production requirements

Empty vectors emit no events (not synthetic zeros). Query failures, warnings,
non-vector responses, missing customer labels, invalid/negative/non-finite
values, and mismatched timestamps are rejected, rather than sent as usage.
The generate input's default nack replay retries the original fixed query
body; partial successes can be resent with the same IDs. An invalid series can
therefore block/retry a whole poll until fixed. Monitor errors and empty results.

Before billing real customers:

- Persist a window cursor and pending events; the generate input is memory-only.
- Backfill every missed window after downtime; this config only polls the latest.
- Deduplicate HA Prometheus replicas and overlapping collectors before aggregation.
- Account for scrape loss, late samples, retention, resets, and extrapolation.
- Add the authentication/TLS settings required by your Prometheus deployment.
- Size customer-label cardinality and preserve all pricing dimensions.
- Settle window-boundary ownership with your billing periods and test it explicitly.

Prometheus can support **aggregated customer usage billing** when the required
customer identifiers and dimensions exist; raw per-request events are not
always necessary. However, `increase()` extrapolates sampled counters and may
produce fractional estimates. Resets between scrapes can lose usage; raw request
details cannot be reconstructed. A durable source of usage events is preferable
when exact, auditable per-operation billing is required.

## Reproduce the simulation

Requires Python 3.8+, Docker, and the built collector. The helper uses Linux
Docker host networking; Docker Desktop users need host-networking support.
It starts its own labeled counter exporter and a pinned real Prometheus server,
then cleans up both. By default, events go only to a local mock Flexprice sink.

```bash
./bento-flexprice test examples/prometheus/pipeline_test.yml
python3 examples/prometheus/simulate.py

# Full minute cron and 60-second query windows (several minutes):
python3 examples/prometheus/simulate.py --window-seconds 60

# Explicit opt-in to real cloud writes using the environment variables above:
python3 examples/prometheus/simulate.py --cloud
```

Each simulation uses a unique event name/source and two synthetic customer IDs,
so no existing event names or customer usage are reused. It sends three series
over two windows, then replays the first window. Local mode checks stable IDs
at a mock sink, **not server-side deduplication**. Cloud mode queries raw events
to verify storage and checks the same six IDs after a 15-second settling period;
that is an observed deduplication check, not a proof of exactly-once billing.
No customer objects or billing configuration are created. Raw events for unknown
customers do not establish a billable customer/subscription relationship.

Logs, events, and a machine-readable summary are saved under the ignored
`.simulation-artifacts/<run_id>/` directory. Cloud simulation leaves its test
usage events in the environment; use a sandbox and retain these IDs for audit.

## References

- [Bento generate input](https://warpstreamlabs.github.io/bento/docs/components/inputs/generate/)
- [Bento HTTP processor](https://warpstreamlabs.github.io/bento/docs/components/processors/http/)
- [Prometheus instant query API](https://prometheus.io/docs/prometheus/latest/querying/api/#instant-queries)
- [Prometheus increase semantics](https://prometheus.io/docs/prometheus/latest/querying/functions/#increase)
- [Kong's Prometheus collector and billing accuracy warning](https://developer.konghq.com/metering-and-billing/collectors/prometheus/)
- [Flexprice event ingestion](https://docs.flexprice.io/api-reference/events/ingest-event)
