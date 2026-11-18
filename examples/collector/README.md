# Collector UI (Prometheus → Flexprice)

`bento-flexprice collector` lets you set up a Prometheus collector in a UI instead of hand-writing Bento YAML:
connect, write PromQL, map rows to events, preview against live data, save a version, run.
One binary and image runs everywhere: next to a private Prometheus (self-hosted) or on Flexprice's ECS (hosted).

```
 UI ── /api ── spec store (files, optional git)      secrets (env vars from the deployment, or files)
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

Common flags: `--data` (`COLLECTOR_DATA_DIR`), `--secrets` (`COLLECTOR_SECRETS_DIR`, default `<data>/../collector-secrets`, must be outside the data dir), `--git` (`COLLECTOR_GIT=true`: commit each save), `--connections` (`COLLECTOR_CONNECTIONS_FILE`) and `--lock-connections` (`COLLECTOR_LOCK_CONNECTIONS=true`), see below.

Runs happen when the scheduler starts `run` (the CronJob/ECS schedule); a collector's `trigger.schedule` records the intended time but does not start anything by itself. `collector render` exports use the Flexprice output plugin, which has no custom headers, so `x-environment-id` is not carried over (use an environment-scoped API key).

`run` exits **1** when any collector/day failed or rejected rows, so the scheduler retries and alerts. It exits 0 only when everything was sent.

## What a run does

1. Completeness gate (optional): evaluates e.g. `count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440` at the end of the day. Below the minimum → **nothing is sent**.
2. Runs the PromQL at 00:00 UTC after the day (`window` mode) or with `__USAGE_DATE__` substituted (`usage_date` mode).
3. Maps each series: customer from a fixed value, a label, or a label → customer table; required labels; dropped labels; extra properties. NaN/Inf/negative values, missing labels and unmapped tenants are **rejected**; label values listed under "not billed" are **skipped**.
4. Checks Flexprice: a meter must exist for the event name (otherwise nothing is sent) and every customer must exist (otherwise that row is rejected).
5. Sends valid events in bulk. `event_id` is a hash of collector, event name, customer, query, window and labels, and `timestamp` is the day's start, so a re-run or backfill of a day produces the same events.

Preview (UI) runs steps 1–4 against live Prometheus and sends nothing.

## Connections owned by the deployment

For a real deployment, keep connections and credentials with your infrastructure instead of the UI.
The UI then only manages collectors (queries, mapping, tenants) and shows these connections read-only
as "Managed by deployment"; Test still works.

```yaml
# connections.yaml (e.g. a ConfigMap), passed as COLLECTOR_CONNECTIONS_FILE
connections:
  - id: prometheus
    type: prometheus
    url: ${PROMETHEUS_URL}             # ${VAR} is read from the environment
    auth_type: basic                   # none | basic | bearer
    username: collector
    secret_env: PROMETHEUS_PASSWORD    # password/token: the name of an env var, never the value
  - id: flexprice
    type: flexprice
    url: https://api.cloud.flexprice.io
    secret_env: FLEXPRICE_API_KEY
    headers: { x-environment-id: "${FLEXPRICE_ENV_ID}" }   # self-hosted Flexprice only
```

- Give `serve` and the scheduled `run` the same file and env vars (from your Kubernetes Secret, which External Secrets Operator can sync from AWS/GCP Secrets Manager or Vault).
- Startup fails and lists every variable that is not set, so a missing credential is found at deploy time, not at the first run.
- `COLLECTOR_LOCK_CONNECTIONS=true` hides and refuses connections created in the UI. Without it, UI connections (with secrets in the secret store) can be added next to the managed ones, which is handy for local trials.
- Collectors refer to connections by `id`, so the same saved collectors work against staging and production deployments with different files.
- Only connections in this file can read environment variables; a connection created in the UI cannot point `secret_ref` at one.
- `collector render` writes these secrets as `${PROMETHEUS_PASSWORD}` / `${FLEXPRICE_API_KEY}` (the `secret_env` names).

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
| Kubernetes, next to Prometheus | [deploy/kubernetes/collector.yaml](../../deploy/kubernetes/collector.yaml): connections ConfigMap, UI Deployment + daily CronJob (`run --all`) + PVC; credentials from your Secret as env vars |
| One machine | [deploy/docker-compose/docker-compose.yml](../../deploy/docker-compose/docker-compose.yml), then `docker compose run --rm collector-ui collector run --all` from cron |
| Flexprice-hosted (ECS) | Same image: an ECS service runs `collector serve` (internal only), an EventBridge-scheduled task runs `collector run --all`; both mount the same EFS volume at `/data`, secrets from Secrets Manager as files under `/secrets`. Needs network access to the customer's Prometheus |

## Try it: Syncron sandbox

[syncron-sandbox/](syncron-sandbox/) plays Syncron end to end with a real Prometheus (basic auth, 7 days of Syncron-shaped data with a metrics gap, a negative value and an unmapped tenant) and a local Flexprice.

**One command** (Prometheus with the sandbox data plus the collector UI built from this repo):

```bash
docker compose -f examples/collector/syncron-sandbox/docker-compose.yml up -d --build
# UI: http://localhost:8090, token syncron-ui-token
# Prometheus connection: http://prometheus:9090, basic auth syncron / syncron-secret
# Flexprice connection (local): http://host.docker.internal:8080
docker compose -f examples/collector/syncron-sandbox/docker-compose.yml run --rm --no-deps collector collector run --all
```

**Without Docker Compose:**

```bash
examples/collector/syncron-sandbox/up.sh                       # Prometheus on :9095 (needs bcrypt; uses local prometheus/promtool or Docker)
FLEXPRICE_API_KEY=… FLEXPRICE_ENV_ID=… python3 examples/collector/syncron-sandbox/flexprice_setup.py
COLLECTOR_UI_TOKEN=dev ./bento-flexprice collector serve --data ./collector-data &
COLLECTOR_UI_TOKEN=dev COLLECTOR_DATA_DIR=./collector-data BENTO_FLEXPRICE=./bento-flexprice \
  FLEXPRICE_API_KEY=… FLEXPRICE_ENV_ID=… python3 examples/collector/syncron-sandbox/scenarios.py
```

## Check that Prometheus and Flexprice agree

Compare one tenant and one closed UTC day. Run the collector's query in Prometheus at the end of the day, then ask Flexprice for the same meter, customer and day:

```bash
PROM=http://localhost:9095 FP=http://localhost:8080   # your Prometheus and Flexprice API
FPH=(-H "x-api-key: $FLEXPRICE_API_KEY" -H "x-environment-id: $FLEXPRICE_ENV_ID")   # drop x-environment-id on Flexprice Cloud
DAY=2026-11-16; END=$(date -u -d "$DAY + 1 day" +%s)
# 1. Prometheus: the value the collector sends (sum over every series of that tenant)
curl -s -u syncron:syncron-secret "$PROM/api/v1/query" --data-urlencode "time=$END" --data-urlencode \
  'query=sum(sum_over_time(dp_dsi_pod_cpu_requests_cores{label_tags_syncron_com_sy_tenant="trn_tQAAE7UDDQTNFS"}[1d]) / 60)' \
  | jq -r '.data.result[0].value[1]'
# 2. Flexprice: usage for the meter whose event_name the collector sends
METER=$(curl -s "$FP/v1/meters?limit=500" "${FPH[@]}" | jq -r '.items[] | select(.event_name=="syncron_cpu_hours") | .id')
curl -s "$FP/v1/events/usage/meter" "${FPH[@]}" -H 'Content-Type: application/json' \
  -d "{\"meter_id\":\"$METER\",\"external_customer_id\":\"cust_acme\",\"start_time\":\"${DAY}T00:00:00Z\",\"end_time\":\"$(date -u -d "$DAY + 1 day" +%F)T00:00:00Z\"}" \
  | jq .value
```

The two numbers should be equal (sandbox: 168 for Acme). If they are not:

- **Flexprice lower:** the run was blocked (completeness, missing meter) or rows were rejected; `collector run` prints both. Ingestion is asynchronous, so wait a minute before comparing.
- **Flexprice higher:** the meter is COUNT instead of SUM of `properties.value`, or two collectors send the same `event_name` for overlapping data.
- **Off by a day:** events are stamped at the window start by default; ask Flexprice for the same UTC day, not the next one.

`scenarios.py` runs this check automatically for the sandbox.

## Not in this version

Run history screen, cursor and missed-day alert (use the scheduler's job-failure alert), approvals, collector-token config pull, writing secrets to Kubernetes/Secrets Manager from the UI, sources other than Prometheus.

## Developing the UI

The UI source is a React + Tailwind app in `collector/web`, built from the FlexPrice console's Base UI primitives and ReUI components (`src/components/primitives`, `src/components/reui`, vendored from `flexprice/portal`). The build output in `collector/ui` is committed and embedded into the Go binary, so a plain `go build` needs no Node.

```bash
cd collector/web
npm ci
npm run dev      # Vite dev server; proxies /api to 127.0.0.1:8090
npm run build    # typecheck, then write collector/ui
```

Rebuild and commit `collector/ui` after changing anything in `collector/web`.
