# Collector UI (Prometheus → Flexprice)

`bento-flexprice collector` lets you set up a Prometheus collector in a UI instead of hand-writing Bento YAML:
connect, write PromQL, map rows to events, preview against live data, save a version, run.
One binary and image runs everywhere: next to a private Prometheus (self-hosted) or on Flexprice's ECS (hosted).

```
 UI ── /api ── spec store (files, optional git)      secret store (files / mounted K8s Secret)
                 │                                        │
                 ▼                                        ▼
        collector run --all  ── renders spec → Bento pipeline ── PromQL ──▶ Prometheus
        (CronJob / ECS task)                    └── validated events ──▶ Flexprice /v1/events/bulk
```

## Commands

| Command | What it does |
|---|---|
| `collector serve [--addr :8090]` | UI + API. Set `COLLECTOR_UI_TOKEN`; without it the UI is open to anyone who can reach it |
| `collector run --collector <id> \| --all [--day YYYY-MM-DD] [--from YYYY-MM-DD] [--dry-run]` | Runs one closed UTC day (default yesterday) or a backfill range, prints one JSON result per collector/day |
| `collector render --collector <id> [--version N] [--day …]` | Prints the full Bento config (secrets as `${ENV}`), runnable with `bento-flexprice -c` |

Common flags: `--data` (`COLLECTOR_DATA_DIR`), `--secrets` (`COLLECTOR_SECRETS_DIR`, default `<data>/../collector-secrets`, must be outside the data dir), `--git` (`COLLECTOR_GIT=true`: commit each save).

`run` exits **1** when any collector/day failed or rejected rows, so the scheduler retries and alerts. It exits 0 only when everything was sent.

## What a run does

1. Completeness gate (optional): evaluates e.g. `count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440` at the end of the day. Below the minimum → **nothing is sent**.
2. Runs the PromQL at 00:00 UTC after the day (`window` mode) or with `__USAGE_DATE__` substituted (`usage_date` mode).
3. Maps each series: customer from a fixed value, a label, or a label → customer table; required labels; dropped labels; extra properties. NaN/Inf/negative values, missing labels and unmapped tenants are **rejected**; label values listed under "not billed" are **skipped**.
4. Checks Flexprice: a meter must exist for the event name (otherwise nothing is sent) and every customer must exist (otherwise that row is rejected).
5. Sends valid events in bulk. `event_id` is a hash of collector, event name, customer, query, window and labels, and `timestamp` is the day's start, so a re-run or backfill of a day produces the same events.

Preview (UI) runs steps 1–4 against live Prometheus and sends nothing.

## Storage

```
<data>/connections/<id>.json          url, auth type, secret_ref (never the secret)
<data>/collectors/<id>/v0001.json     immutable versions; runs use the latest
<secrets>/<secret_ref>                one file per secret (0600), or a mounted Kubernetes Secret
```

With `--git`, the data dir is a git repo and every save is a commit, which gives history and an audit log.

## Deploy

| Mode | How |
|---|---|
| Kubernetes, next to Prometheus | [deploy/kubernetes/collector.yaml](../../deploy/kubernetes/collector.yaml): UI Deployment + daily CronJob (`run --all`) + PVC + Secret |
| One machine | [deploy/docker-compose/docker-compose.yml](../../deploy/docker-compose/docker-compose.yml), then `docker compose run --rm collector-ui collector run --all` from cron |
| Flexprice-hosted (ECS) | Same image: an ECS service runs `collector serve` (internal only), an EventBridge-scheduled task runs `collector run --all`; both mount the same EFS volume at `/data`, secrets from Secrets Manager as files under `/secrets`. Needs network access to the customer's Prometheus |

## Try it: Syncron sandbox

[syncron-sandbox/](syncron-sandbox/) plays Syncron end to end with a real Prometheus (basic auth, 7 days of Syncron-shaped data with a metrics gap, a negative value and an unmapped tenant) and a local Flexprice.

```bash
examples/collector/syncron-sandbox/up.sh                       # Prometheus on :9095 (needs bcrypt; uses local prometheus/promtool or Docker)
FLEXPRICE_API_KEY=… FLEXPRICE_ENV_ID=… python3 examples/collector/syncron-sandbox/flexprice_setup.py
COLLECTOR_UI_TOKEN=dev ./bento-flexprice collector serve --data ./collector-data &
COLLECTOR_UI_TOKEN=dev COLLECTOR_DATA_DIR=./collector-data BENTO_FLEXPRICE=./bento-flexprice \
  FLEXPRICE_API_KEY=… FLEXPRICE_ENV_ID=… python3 examples/collector/syncron-sandbox/scenarios.py
```

## Not in this version

Run history screen, cursor and missed-day alert (use the scheduler's job-failure alert), approvals, collector-token config pull, writing secrets to Kubernetes/Secrets Manager from the UI, sources other than Prometheus.
