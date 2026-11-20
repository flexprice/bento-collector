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
| `collector render --collector <id> [--version N] [--window START]` | Prints the stream config publish writes, or with `--window` (UTC `YYYY-MM-DD` or `YYYY-MM-DDTHH:MM`) a one-off config for that window (backfill) |
| `bento-flexprice -w streams --no-api DIR` | Plain Bento: runs one stream per published collector and applies publish/unpublish without a restart |

Common flags: `--connections` (`COLLECTOR_CONNECTIONS_FILE`, required, see below), `--data` (`COLLECTOR_DATA_DIR`) and `--git` (`COLLECTOR_GIT=true`: commit each save, publish and delete).

## Setting up a collector

| Step | What you set |
|---|---|
| Query | Prometheus connection, mode, window and PromQL. **Range** mode: the window is 1, 2, 3, 4, 6, 8, 12 or 24 hours (aligned to UTC) and `[__WINDOW__]` in the query becomes that range. **Dated snapshot**: the metric holds a total per day in a `usage_date` label; `__USAGE_DATE__` becomes the day. Sample runs the query over a From/To range (or one day for snapshots) to list its labels and values |
| Map to events | The event name, picked from the Flexprice meters (the meter also sets the property the value is sent as). Event properties: only the properties you add are sent, each from a label of the query or a default value (name them after the meter's filters). Customer: a label whose value is sent as is as the external customer ID, or one fixed customer. To leave customers out, filter them in the PromQL. Timestamp: window start (default) or end |
| Schedule | Derived from the window: e.g. every hour at :05 UTC, every 6 hours at :05, daily at 00:15. Optional completeness check |
| Preview & save | Runs the rendered pipeline for one window against live Prometheus and checks Flexprice (meter, value property, filter properties, customers). Sends nothing. Save writes the next immutable version |

## Save, preview, publish

| Action | What happens |
|---|---|
| Save | Writes the next immutable version. Does not change what runs |
| Publish vN | Checks the meter exists and reads the value property (otherwise publish is refused), then atomically writes `<streams>/<id>.yaml`. Bento restarts that stream with vN |
| Roll back | Publish an older version |
| Unpublish | Deletes the stream file; Bento stops the stream |
| Delete | Unpublishes and removes every version |
| Backfill | `collector render --collector <id> --window 2026-11-07T13:00 > w.yaml && bento-flexprice -c w.yaml` (same `event_id`s, so a window already sent is not double-billed) |

## What a published collector does each window

At its schedule (UTC; publishing never triggers a run), for the newest window that closed at least the delay (5 minutes, 15 for daily windows) earlier:

1. Completeness check (optional): below the minimum, **nothing is sent** for the window and Bento logs the error.
2. Runs the PromQL at the window end (range mode) or with `__USAGE_DATE__` filled in (dated snapshot).
3. Maps each row to one event: customer from the label value (or the fixed customer), the value under the meter's property, and the event properties you added. NaN/Inf/negative values, a missing customer label and a missing property label are **rejected** and logged. Customers that do not exist in Flexprice are still sent: Flexprice stores the events and bills them once the customer exists.
4. Sends valid events in batches. `event_id` is a hash of collector, event name, customer, query, window and labels, and `timestamp` is the window start, so a backfill of a window produces the same events.

Rejected or failed windows are not replayed (`auto_replay_nacks: false`); fix the spec or data, publish, and backfill the window.

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

Run history, cursor and missed-window alert (a tick missed while Bento is down is not caught up; alert on Bento's logs/metrics and backfill), approvals, managing connections, secrets or meters in the UI, sources other than Prometheus.

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
