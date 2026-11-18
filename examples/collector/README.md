# Collector UI (Prometheus → Flexprice)

`bento-flexprice collector` manages Bento configs for Prometheus collectors: set one up in a UI
(connect, write PromQL, map rows to events, preview against live data), save versions, and publish one.
Publishing writes the rendered stream config to a directory that plain Bento runs in streams mode, so the
collector manages configs and Bento runs them. One image runs both processes.

```
 UI ── /api ── spec store (versions, optional git)        connections file + secrets as env vars (from the deployment)
                 │ publish vN
                 ▼
        <streams>/<id>.yaml ──▶ bento-flexprice -w streams --no-api <streams>
                                   ├── PromQL ──▶ Prometheus
                                   └── events ──▶ Flexprice /v1/events/bulk
```

## Commands

| Command | What it does |
|---|---|
| `collector serve [--addr :8090] [--streams DIR]` | UI + API. Publish writes `DIR/<id>.yaml` (default `<data>/streams`). There is no login: anyone who can reach the port can edit and publish collectors, so keep it internal or behind your own auth proxy |
| `collector render --collector <id> [--version N] [--day YYYY-MM-DD]` | Prints the stream config publish writes, or with `--day` a one-off config for that day (backfill) |
| `bento-flexprice -w streams --no-api DIR` | Plain Bento: runs one stream per published collector and applies publish/unpublish without a restart |

Common flags: `--connections` (`COLLECTOR_CONNECTIONS_FILE`, required, see below), `--data` (`COLLECTOR_DATA_DIR`) and `--git` (`COLLECTOR_GIT=true`: commit each save and publish).

## Save, preview, publish

| Step | What happens |
|---|---|
| Preview | Runs the rendered pipeline for a past day against live Prometheus and checks Flexprice (meter, customers). Sends nothing |
| Save | Writes the next immutable version. Does not change what runs |
| Publish vN | Checks Flexprice (the meter for the event name and every customer the spec names must exist, otherwise publish is refused), then atomically writes `<streams>/<id>.yaml`. Bento restarts that stream with vN |
| Roll back | Publish an older version |
| Unpublish | Deletes the stream file; Bento stops the stream |
| Backfill | `collector render --collector <id> --day 2026-11-07 > day.yaml && bento-flexprice -c day.yaml` (same `event_id`s, so a day already sent is not double-billed) |

## What a published collector does each day

At `trigger.schedule` (UTC; the cron `generate` input waits for its next tick, so publishing never triggers a run):

1. Completeness gate (optional): evaluates e.g. `count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440` at the end of the previous day. Below the minimum → **nothing is sent** and Bento logs the error.
2. Runs the PromQL at 00:00 UTC after the day (`window` mode) or with `__USAGE_DATE__` substituted (`usage_date` mode).
3. Maps each series: customer from a fixed value, a label, or a label → customer table; required labels; dropped labels; extra properties. NaN/Inf/negative values, missing labels and unmapped tenants are **rejected** and logged; label values listed under "not billed" are **skipped**.
4. Sends valid events in batches. `event_id` is a hash of collector, event name, customer, query, window and labels, and `timestamp` is the day's start, so a backfill of a day produces the same events.

Rejected or failed days are not replayed (`auto_replay_nacks: false`); fix the spec or data, publish, and backfill the day.

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

- Give `serve` the file and env vars, and give the Bento container the same env vars (from your Kubernetes Secret, which External Secrets Operator can sync from AWS/GCP Secrets Manager or Vault). Bento rejects a stream whose `${VAR}` is unset.
- Startup fails and lists every variable that is not set, so a missing credential is found at deploy time, not at the first run.
- Collectors refer to connections by `id`. List several (e.g. `flexprice-sandbox` and `flexprice-prod`, each with its own `secret_env`) to choose per collector, or give each environment's deployment its own file with the same ids so saved collectors move between them unchanged.
- To add or change a connection, edit the file and redeploy.
- Rendered and published configs carry these secrets as `${PROMETHEUS_PASSWORD}` / `${FLEXPRICE_API_KEY}` (the `secret_env` names); Bento fills them from its environment.

## Storage

```
<data>/collectors/<id>/v0001.json       immutable versions
<data>/collectors/<id>/published.json   which version is published, when, by whom
<streams>/<id>.yaml                     the published stream config Bento runs (default <data>/streams)
```

With `--git`, the data dir is a git repo and every save and publish is a commit, which gives history and an audit log.

## Deploy

| Mode | How |
|---|---|
| Kubernetes, next to Prometheus | [deploy/kubernetes/collector.yaml](../../deploy/kubernetes/collector.yaml): connections ConfigMap, one Deployment with the UI and a Bento streams container sharing a PVC; credentials from your Secret as env vars |
| One machine | [deploy/docker-compose/](../../deploy/docker-compose/): edit `connections.yaml`, put the secrets in `.env`, `docker compose up -d` (UI + Bento streams) |
| Flexprice-hosted (ECS) | Same image: one ECS task with two containers (`collector serve`, internal only, and `-w streams --no-api /data/streams`) sharing an EFS volume at `/data`; secrets from Secrets Manager as task env vars (`secret_env`). Needs network access to the customer's Prometheus. Not built or tested |

## Not in this version

Run history screen, cursor and missed-day alert (a tick missed while Bento is down is not caught up; alert on Bento's logs/metrics and backfill), approvals, collector-token config pull, managing connections or secrets in the UI, sources other than Prometheus.

## Code layout

```
collector/
  cli.go      serve / render commands (called from main.go)
  spec/       Collector, Connection, Window types and validation
  store/      FileStore (versions, published version and stream file, git) and the connections file
  source/     Provider interface + Registry: what an input source must implement
    prometheus/   Prometheus provider: HTTP client, Bento fetch stages, completeness
  flexprice/  destination: API client (meter and customer checks) and Bento output
  render/     generic pipeline: window → provider stages → normalized row → event mapping (golden tests in testdata/)
  engine/     preview and publish: resolves provider + connections, runs the rendered pipeline in-process, Flexprice checks
  server/     HTTP API, embedded UI
  web/        React UI: src/, dist/ (built, embedded by embed.go)
```

Dependencies point one way: `spec` ← `source`, `flexprice`, `store` ← `render` ← `engine` ← `server` ← `cli.go`. A source provider emits one message per row, normalized to `{"labels": {...}, "value": "...", "at": <unix>}`; everything after that is shared.

**Adding a source** (e.g. Datadog, ClickHouse): add `collector/source/<name>/` implementing `source.Provider` (`ValidateConnection`, `ValidateSource`, `Test`, `Stages`, `Gate`) and register it in `cli.go` (`source.NewRegistry(prometheus.Provider{}, <name>.Provider{})`). Connections of that `type` and collectors with that `source.type` then work in the UI, preview, publish and `render`.

## Developing the UI

The UI source is a React + Tailwind app in `collector/web`, built from the FlexPrice console's Base UI primitives and ReUI components (`src/components/primitives`, `src/components/reui`, vendored from `flexprice/portal`). The build output in `collector/web/dist` is committed and embedded into the Go binary, so a plain `go build` needs no Node.

```bash
cd collector/web
npm ci
npm run dev      # Vite dev server; proxies /api to 127.0.0.1:8090
npm run build    # typecheck, then write collector/web/dist
```

Rebuild and commit `collector/web/dist` after changing anything in `collector/web`.
