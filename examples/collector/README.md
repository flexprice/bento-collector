# Collector UI (Prometheus → Flexprice)

`bento-flexprice collector` lets you set up a Prometheus collector in a UI instead of hand-writing Bento YAML:
connect, write PromQL, map rows to events, preview against live data, save a version, run.
One binary and image runs everywhere: next to a private Prometheus (self-hosted) or on Flexprice's ECS (hosted).

```
 UI ── /api ── spec store (files, optional git)      connections file + secrets as env vars (from the deployment)
                 │                                        │
                 ▼                                        ▼
        collector run --all  ── renders spec → Bento pipeline ── PromQL ──▶ Prometheus
        (CronJob / ECS task)                    └── validated events ──▶ Flexprice /v1/events/bulk
```

## Commands

| Command | What it does |
|---|---|
| `collector serve [--addr :8090]` | UI + API. There is no login: anyone who can reach the port can edit collectors, so keep it internal or behind your own auth proxy |
| `collector run --collector <id> \| --all [--day YYYY-MM-DD] [--from YYYY-MM-DD] [--dry-run]` | Runs one closed UTC day (default yesterday) or a backfill range, prints one JSON result per collector/day |
| `collector render --collector <id> [--version N] [--day …]` | Prints the full Bento config (secrets as `${ENV}`), runnable with `bento-flexprice -c` |

Common flags: `--connections` (`COLLECTOR_CONNECTIONS_FILE`, required, see below), `--data` (`COLLECTOR_DATA_DIR`) and `--git` (`COLLECTOR_GIT=true`: commit each save).

Runs happen when the scheduler starts `run` (the CronJob/ECS schedule); a collector's `trigger.schedule` records the intended time but does not start anything by itself. `collector render` exports use the Flexprice output plugin, which has no custom headers, so `x-environment-id` is not carried over (use an environment-scoped API key).

`run` exits **1** when any collector/day failed or rejected rows, so the scheduler retries and alerts. It exits 0 only when everything was sent.

## What a run does

1. Completeness gate (optional): evaluates e.g. `count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440` at the end of the day. Below the minimum → **nothing is sent**.
2. Runs the PromQL at 00:00 UTC after the day (`window` mode) or with `__USAGE_DATE__` substituted (`usage_date` mode).
3. Maps each series: customer from a fixed value, a label, or a label → customer table; required labels; dropped labels; extra properties. NaN/Inf/negative values, missing labels and unmapped tenants are **rejected**; label values listed under "not billed" are **skipped**.
4. Checks Flexprice: a meter must exist for the event name (otherwise nothing is sent) and every customer must exist (otherwise that row is rejected).
5. Sends valid events in bulk. `event_id` is a hash of collector, event name, customer, query, window and labels, and `timestamp` is the day's start, so a re-run or backfill of a day produces the same events.

Preview (UI) runs steps 1–4 against live Prometheus and sends nothing.

## Connections

Connections and credentials are owned by the deployment, not the UI. The UI only manages collectors
(queries, mapping, tenants) and lists the connections read-only; Test still works.

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
- Collectors refer to connections by `id`. List several (e.g. `flexprice-sandbox` and `flexprice-prod`, each with its own `secret_env`) to choose per collector, or give each environment's deployment its own file with the same ids so saved collectors move between them unchanged.
- To add or change a connection, edit the file and redeploy.
- `collector render` writes these secrets as `${PROMETHEUS_PASSWORD}` / `${FLEXPRICE_API_KEY}` (the `secret_env` names).

## Storage

```
<data>/collectors/<id>/v0001.json     immutable versions; runs use the latest
```

With `--git`, the data dir is a git repo and every save is a commit, which gives history and an audit log.

## Deploy

| Mode | How |
|---|---|
| Kubernetes, next to Prometheus | [deploy/kubernetes/collector.yaml](../../deploy/kubernetes/collector.yaml): connections ConfigMap, UI Deployment + daily CronJob (`run --all`) + PVC; credentials from your Secret as env vars |
| One machine | [deploy/docker-compose/](../../deploy/docker-compose/): edit `connections.yaml`, put the secrets in `.env`, `docker compose up -d`, then `docker compose run --rm collector-ui collector run --all` from cron |
| Flexprice-hosted (ECS) | Same image: an ECS service runs `collector serve` (internal only), an EventBridge-scheduled task runs `collector run --all`; both mount the same EFS volume at `/data` and the connections file; secrets from Secrets Manager as task env vars (`secret_env`). Needs network access to the customer's Prometheus |

## Not in this version

Run history screen, cursor and missed-day alert (use the scheduler's job-failure alert), approvals, collector-token config pull, managing connections or secrets in the UI, sources other than Prometheus.

## Code layout

```
collector/
  cli.go      serve / run / render commands (called from main.go)
  spec/       Collector, Connection, Window types and validation
  store/      FileStore (versions, git) and the connections file
  render/     spec → Bento pipeline config (golden test in testdata/)
  engine/     preview and run: completeness, in-process Bento pipeline, Flexprice checks, send
  server/     HTTP API, auth, embedded UI
  web/        React UI: src/, dist/ (built, embedded by embed.go)
```

Dependencies point one way: `spec` ← `store`, `render` ← `engine` ← `server` ← `cli.go`.

## Developing the UI

The UI source is a React + Tailwind app in `collector/web`, built from the FlexPrice console's Base UI primitives and ReUI components (`src/components/primitives`, `src/components/reui`, vendored from `flexprice/portal`). The build output in `collector/web/dist` is committed and embedded into the Go binary, so a plain `go build` needs no Node.

```bash
cd collector/web
npm ci
npm run dev      # Vite dev server; proxies /api to 127.0.0.1:8090
npm run build    # typecheck, then write collector/web/dist
```

Rebuild and commit `collector/web/dist` after changing anything in `collector/web`.
