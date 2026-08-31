# Bento Flexprice Collector

[![CI](https://github.com/flexprice/bento-collector/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/flexprice/bento-collector/actions/workflows/ci.yml)
[![Publish Image](https://github.com/flexprice/bento-collector/actions/workflows/publish-image.yml/badge.svg)](https://github.com/flexprice/bento-collector/actions/workflows/publish-image.yml)
[![GHCR](https://ghcr-badge.egpl.dev/flexprice/bento-collector/latest_tag?trim=major&label=ghcr.io)](https://github.com/flexprice/bento-collector/pkgs/container/bento-collector)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A custom [Bento](https://github.com/warpstreamlabs/bento) distribution that enables streaming usage events to [Flexprice](https://flexprice.io) from **any data source** — Kafka, databases, HTTP APIs, files, and [200+ more connectors](https://warpstreamlabs.github.io/bento/docs/components/inputs/about).

Built on the official Flexprice Go SDK, this collector handles event transformation, batching, retries, and dead-letter queues out of the box.

> **Open Source**: Uses [Bento](https://github.com/warpstreamlabs/bento) (MIT license) — no vendor lock-in.

## Features

- **Flexprice Output Plugin** — Uses official Flexprice Go SDK, with automatic single/bulk endpoint selection
- **Any Input Source** — Kafka, AWS MSK, AWS SQS, Azure Event Hubs, Google Managed Kafka, PostgreSQL, HTTP, S3, and 200+ connectors
- **Cloud OAuth Plugins** — Custom cache plugins for Azure Managed Identity (`azure_eventhub_oauth`) and Google Managed Kafka (`gcp_managed_kafka_oauth`), so no static broker secrets are needed
- **Bloblang Transforms** — Transform and enrich events on-the-fly
- **Built-in Reliability** — Retry logic, batching, and dead-letter queue support
- **Docker Ready** — Multi-arch image (`linux/amd64`, `linux/arm64`) published to GHCR

---

## How to use this collector

Everything here follows the same three steps. Pick your path, then jump to the section:

1. **[Get the binary or image](#1-get-the-binary-or-image)** — build from source or pull from GHCR
2. **[Pick a config for your source](#2-pick-a-config-for-your-source)** — a ready-made YAML per source
3. **[Set the environment variables](#3-set-the-environment-variables)** — every config is fully env-driven

Configs are never edited to change environment, credentials, or auth mode — only env vars change. The same file works in local dev and production.

---

## 1. Get the binary or image

**Build from source:**

```bash
go build -o bento-flexprice main.go
```

**Or pull the published image** (multi-arch: `linux/amd64`, `linux/arm64`):

```bash
docker pull ghcr.io/flexprice/bento-collector:latest

# or pin a release (recommended for production)
docker pull ghcr.io/flexprice/bento-collector:v1.2.2
```

The badge at the top of this README always shows the newest published tag. All tags are listed on the [package page](https://github.com/flexprice/bento-collector/pkgs/container/bento-collector).

---

## 2. Pick a config for your source

### Start here — [examples/](examples/)

Generic, runnable configs meant as starting points.

| Config | Flow | Notes |
|--------|------|-------|
| [dummy-events-to-flexprice.yaml](examples/dummy-events-to-flexprice.yaml) | generate → Flexprice | No infrastructure needed — start here |
| [generate-to-kafka.yaml](examples/kafka/generate-to-kafka.yaml) | generate → Kafka | Produces test events for the two below |
| [consume-from-kafka.yaml](examples/kafka/consume-from-kafka.yaml) | Kafka → Flexprice | Simple consume |
| [consume-from-kafka-with-dlq.yaml](examples/kafka/consume-from-kafka-with-dlq.yaml) | Kafka → Flexprice + DLQ | Recommended for production |

### Cloud-managed sources — [internal/](internal/)

Production configs we run ourselves. Useful directly, or as references when self-hosting.

| Config | Flow | Auth |
|--------|------|------|
| [aws-kafka-to-flexprice.yaml](internal/aws-kafka-to-flexprice.yaml) | AWS MSK → Flexprice | All listener types — see [MSK auth modes](#aws-msk-auth-modes) |
| [aws-sqs-to-flexprice.yaml](internal/aws-sqs-to-flexprice.yaml) | AWS SQS → Flexprice | IAM role (EC2/ECS/EKS) or static keys |
| [azure-eh-to-flexprice.yaml](internal/azure-eh-to-flexprice.yaml) | Azure Event Hubs → Flexprice | SAS connection string |
| [azure-eh-to-flexprice-mi.yaml](internal/azure-eh-to-flexprice-mi.yaml) | Azure Event Hubs → Flexprice | Managed Identity (no stored secret) |

### Internal pipelines — [internal/](internal/)

Flexprice-specific pipelines. Listed so the repo is self-describing; most users will not need them.

| Config | Flow | Purpose |
|--------|------|---------|
| [custom-kafka-to-flexprice.yaml](internal/custom-kafka-to-flexprice.yaml) | Kafka → Flexprice | BillingEntry event pipeline |
| [custom-kafka-to-flexprice-kafka.yaml](internal/custom-kafka-to-flexprice-kafka.yaml) | Kafka → Kafka | Republish with retry |
| [custom-kafka-to-flexprice-kafka-backfill-no-model.yaml](internal/custom-kafka-to-flexprice-kafka-backfill-no-model.yaml) | Kafka → Kafka | Backfill for events with no `modelName` |
| [custom-kafka-to-flexprice-clickhouse.yaml](internal/custom-kafka-to-flexprice-clickhouse.yaml) | Kafka → ClickHouse + Kafka | Raw events with retry + DLQ |
| [custom-kafka-to-flexprice-clickhouse-v2.yaml](internal/custom-kafka-to-flexprice-clickhouse-v2.yaml) | Kafka → ClickHouse | v2: single output, org filtering |
| [custom-kafka-to-flexprice-clickhouse-gmk.yaml](internal/custom-kafka-to-flexprice-clickhouse-gmk.yaml) | Kafka → ClickHouse + Kafka | GMK variant (Google Managed Kafka OAuth) |
| [custom-events-to-kafka.yaml](internal/custom-events-to-kafka.yaml) | generate → Kafka | Generates BillingEntry test events |
| [kafka-to-s3.yaml](internal/kafka-to-s3.yaml) | Kafka → S3 | Archive raw events |
| [custom-kafka-to-s3.yaml](internal/custom-kafka-to-s3.yaml) | Kafka → S3 | Archive BillingEntry events |

### Diagnostics

Smoke tests that connect, log, and drop — they never write to Flexprice.

| Config | Checks |
|--------|--------|
| [azure-eh-authtest.yaml](internal/azure-eh-authtest.yaml) | Azure Event Hubs OAUTHBEARER auth works |
| [gmk-authtest.yaml](internal/gmk-authtest.yaml) | Google Managed Kafka OAUTHBEARER auth works |
| [verify-flexprice-output.yaml](internal/verify-flexprice-output.yaml) | Inspect what a topic actually contains |

Run it:

```bash
./bento-flexprice -c examples/dummy-events-to-flexprice.yaml
```

Or with Docker:

```bash
docker run --rm --env-file .env \
  -v "$PWD/internal:/configs:ro" \
  ghcr.io/flexprice/bento-collector:latest -c /configs/aws-kafka-to-flexprice.yaml
```

---

## 3. Set the environment variables

```bash
cp env.example .env
# edit .env, then:
source .env
```

Every config needs these two:

```bash
FLEXPRICE_API_HOST=api.cloud.flexprice.io
FLEXPRICE_API_KEY=your_api_key_here
```

The rest depend on your source — see the [full reference](#environment-variables-reference) and [env.example](env.example).

### AWS MSK auth modes

The MSK config supports every listener type. Two variables select the mode; **the config file never changes**:

| Listener | Port | `AWS_KAFKA_TLS_ENABLED` | `AWS_KAFKA_SASL_MECHANISM` | Credentials |
|----------|------|--------------------------|-----------------------------|-------------|
| SASL/SCRAM over TLS *(default)* | 9096 | `true` | `SCRAM-SHA-512` | required |
| TLS, no auth | 9094 | `true` | `none` | not needed |
| PLAINTEXT (no TLS, no auth) | 9092 | `false` | `none` | not needed |
| SASL/PLAIN over TLS | 9096 | `true` | `PLAIN` | required |

```bash
# Example: PLAINTEXT listener — no credentials required at all
AWS_KAFKA_BROKERS=b-1.your-cluster.kafka.us-east-1.amazonaws.com:9092
AWS_KAFKA_TOPIC=your-topic
AWS_KAFKA_TLS_ENABLED=false
AWS_KAFKA_SASL_MECHANISM=none
```

> **Security:** PLAINTEXT sends event payloads unencrypted — only use it for brokers reachable solely from inside your VPC. Never combine `PLAIN` with TLS disabled; that puts the password on the wire. For SCRAM, source the credentials from the AWS Secrets Manager secret bound to the cluster rather than committing them.

### Single vs bulk ingestion

The Flexprice output picks its endpoint from the batching policy, set once at startup:

| Setting | 1-event flush | N-event flush |
|---------|---------------|---------------|
| defaults (`FLEXPRICE_BATCH_COUNT=1`, no period) | single-event endpoint | bulk |
| `FLEXPRICE_BATCH_COUNT=500`, `FLEXPRICE_BATCH_PERIOD=5s` | **bulk** | bulk |

`FLEXPRICE_BATCH_COUNT=1` does *not* enable batching — activation needs a count above 1, or a non-empty period. Batches larger than 1000 events are split automatically to respect the bulk API limit.

---

## Event Format

Events must be JSON with these fields (see [Flexprice Ingest Event API](https://docs.flexprice.io/api-reference/events/ingest-event#ingest-event)):

```json
{
  "event_name": "api_request",            // Required: must match a meter name
  "external_customer_id": "cust_123",     // Required: customer ID
  "properties": {                         // Optional: event data for aggregation
    "tokens": 100,
    "model": "gpt-4"
  },
  "timestamp": "2025-12-01T10:30:00Z",    // Optional: defaults to now
  "source": "kafka-stream",               // Optional: event source identifier
  "event_id": "evt_123"                   // Optional: unique event ID
}
```

**⚠️ SDK Note:** Numeric property values must be converted to strings in your Bloblang transform using `.string()` or `"%v".format(this.value)` — the Flexprice API will convert them back to numbers for aggregation.

---

## Kafka Testing Flow

The full list of configs is in [Pick a config for your source](#2-pick-a-config-for-your-source). To exercise a Kafka round-trip locally:

```bash
# Step 1: Generate test events to Kafka
./bento-flexprice -c examples/kafka/generate-to-kafka.yaml

# Step 2: Consume from Kafka and send to Flexprice
./bento-flexprice -c examples/kafka/consume-from-kafka.yaml

# Or with Dead Letter Queue support (recommended for production)
./bento-flexprice -c examples/kafka/consume-from-kafka-with-dlq.yaml
```

> **Note:** Create Kafka topics as per config files to ensure you're reading from and writing to the correct cluster.

---

## Monitoring

Bento exposes metrics on port **4195**:

| Endpoint | Description |
|----------|-------------|
| `http://localhost:4195/metrics` | Prometheus metrics |
| `http://localhost:4195/ping` | Health check |
| `http://localhost:4195/stats` | Runtime statistics |

---

## Troubleshooting

### Events not showing in Flexprice UI

**Check 1: Event name matches meter**

```bash
# In Bento logs, look for:
INFO[...] 📤 Sending event: api_request for customer: cust_...
```

The `event_name` must match a meter's `event_name` in Flexprice.

**Check 2: Properties format**

If your meter aggregates a property (e.g., `SUM`), the value should be numeric in the original event:

```json
{
  "event_name": "api_request",
  "properties": {
    "tokens": 100    // ✅ Number in source (converted to string by transform)
  }
}
```

**Check 3: Customer exists**

The `external_customer_id` must match an existing customer in Flexprice.

**Check 4: API response**

Look for errors in logs:

```bash
# Success:
INFO[...] ✅ Event accepted successfully, ID: evt_xxx

# Failure:
ERROR[...] Failed to send event: 400 Bad Request
```

### Kafka not connecting

**Confluent Cloud:**
- Verify `FLEXPRICE_KAFKA_BROKERS` includes the port (`:9092`)
- Check SASL credentials are correct
- Ensure TLS is enabled in config

**AWS MSK:**
- The port must match the auth mode — `:9096` SASL, `:9094` TLS-only, `:9092` PLAINTEXT. A mismatch between port and [auth mode](#aws-msk-auth-modes) is the most common failure.
- Broker hangs with no error: the security group usually isn't allowing the collector's traffic on that port.
- `SASL` / `SCRAM` errors while intending plaintext: `AWS_KAFKA_SASL_MECHANISM` isn't set to `none`.
- Enable the cluster's listener before pointing at it — MSK does not expose all four listeners by default.

**Local Kafka:**
- Use `localhost:29092` (from host) or `kafka:9092` (from Docker)
- Check topic exists: `kafka-topics --list --bootstrap-server localhost:29092`

### Build errors

```bash
# Clean and rebuild
go mod tidy
go build -o bento-flexprice main.go
```

---

## Project Structure

```
bento-collector/
├── main.go                              # Entry point — registers plugins, runs Bento CLI
├── output/
│   ├── flexprice.go                     # Flexprice output plugin (single + bulk)
│   ├── azure_eh_oauth_cache.go          # `azure_eventhub_oauth` cache plugin
│   └── gmk_oauth_cache.go               # `gcp_managed_kafka_oauth` cache plugin
├── examples/                            # Generic starting-point configs
│   ├── dummy-events-to-flexprice.yaml   # Generate → Flexprice
│   └── kafka/
│       ├── generate-to-kafka.yaml       # Generate → Kafka
│       ├── consume-from-kafka.yaml      # Kafka → Flexprice
│       └── consume-from-kafka-with-dlq.yaml  # Kafka → Flexprice (with DLQ)
├── internal/                            # Configs we run internally (see internal/README.md)
│   ├── aws-kafka-to-flexprice.yaml      # AWS MSK → Flexprice (all auth modes)
│   ├── aws-sqs-to-flexprice.yaml        # AWS SQS → Flexprice (IAM role or keys)
│   ├── azure-eh-to-flexprice*.yaml      # Azure Event Hubs (SAS + Managed Identity)
│   ├── custom-kafka-to-*.yaml           # BillingEntry pipelines (Flexprice/ClickHouse/S3)
│   └── *-authtest.yaml                  # Auth smoke tests (connect, log, drop)
├── scripts/
│   └── build_and_push_ecr.sh            # Build + push to AWS ECR
├── Dockerfile                           # Production container (GHCR + ECR)
├── DEPLOYMENT.md                        # ECR / ECS deployment guide
├── env.example                          # Environment template
└── README.md
```

---

## CI & Releases

Two workflows, in [.github/workflows/](.github/workflows/):

| Workflow | Runs on | Does |
|----------|---------|------|
| [ci.yml](.github/workflows/ci.yml) | PRs to `main`, pushes to `main` | Tests (`-race`), gofmt + `go vet`, `bento lint` on **every** config in `examples/` and `internal/`, and a no-push Docker build |
| [publish-image.yml](.github/workflows/publish-image.yml) | Version tags (`v*`) only | Runs tests, then builds and pushes the multi-arch image to GHCR with SBOM, provenance attestation, and a Trivy scan that fails on HIGH/CRITICAL |

Merging to `main` never publishes an image — only tagging does.

### Cutting a release

```bash
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

That publishes `ghcr.io/flexprice/bento-collector:v1.2.3`. Only a final release (no pre-release suffix) also moves `:latest`, so `v1.2.3-rc.1` publishes without disturbing it.

**Required repo settings:** `secrets.GHCR_PUBLISH_TOKEN` (a PAT with `write:packages`; falls back to `GITHUB_TOKEN` if the package is linked to this repo with Write) and optionally `vars.GHCR_USERNAME` when the PAT owner differs from the tagging user.

---

## How It Works

```
┌───────────────┐
│  Any Input    │  Kafka, PostgreSQL, HTTP, S3, etc.
│   Source      │
└───────┬───────┘
        │
        v
┌───────────────┐
│   Bloblang    │  Transform to Flexprice format
│   Transform   │  (convert properties to strings)
└───────┬───────┘
        │
        v
┌───────────────┐
│   Flexprice   │  Custom output plugin
│    Output     │  (batching, retries, DLQ)
└───────┬───────┘
        │
        v
┌───────────────┐
│  Flexprice    │  Usage data in dashboard
│     API       │
└───────────────┘
```

---

## Environment Variables Reference

**Flexprice API (required by every config):**

| Variable | Description | Example |
|----------|-------------|---------|
| `FLEXPRICE_API_HOST` | Flexprice API host | `api.cloud.flexprice.io` |
| `FLEXPRICE_API_KEY` | API key | `fp_xxx` |
| `FLEXPRICE_BATCH_COUNT` | Events per bulk request (>1 enables bulk) | `500` |
| `FLEXPRICE_BATCH_PERIOD` | Max wait before flushing a batch | `5s` |

**Generic Kafka / Confluent Cloud:**

| Variable | Description | Example |
|----------|-------------|---------|
| `FLEXPRICE_KAFKA_BROKERS` | Kafka brokers | `pkc-xxx.confluent.cloud:9092` |
| `FLEXPRICE_KAFKA_TOPIC` | Kafka topic | `events` |
| `FLEXPRICE_KAFKA_SASL_USER` | SASL username | From Confluent Cloud |
| `FLEXPRICE_KAFKA_SASL_PASSWORD` | SASL password | From Confluent Cloud |
| `FLEXPRICE_KAFKA_CONSUMER_GROUP` | Consumer group | `bento-flexprice-v1` |
| `FLEXPRICE_KAFKA_DLQ_TOPIC` | Dead letter queue topic | `events-dlq` |

**AWS MSK** (see [auth modes](#aws-msk-auth-modes)):

| Variable | Description | Example |
|----------|-------------|---------|
| `AWS_KAFKA_BROKERS` | MSK bootstrap brokers | `b-1.xxx.kafka.us-east-1.amazonaws.com:9096` |
| `AWS_KAFKA_TOPIC` | Topic to consume | `usage-events` |
| `AWS_KAFKA_CONSUMER_GROUP` | Consumer group | `flexprice-bento` |
| `AWS_KAFKA_TLS_ENABLED` | TLS on/off — `false` only for PLAINTEXT | `true` |
| `AWS_KAFKA_SASL_MECHANISM` | `SCRAM-SHA-512` \| `none` \| `PLAIN` \| `AWS_MSK_IAM` | `SCRAM-SHA-512` |
| `AWS_KAFKA_SASL_USER` | SASL username — omit when mechanism is `none` | From Secrets Manager |
| `AWS_KAFKA_SASL_PASSWORD` | SASL password — omit when mechanism is `none` | From Secrets Manager |

See [env.example](env.example) for the complete list, including Azure Event Hubs and consumer tuning.

---

## Support

- **Flexprice Docs**: [docs.flexprice.io](https://docs.flexprice.io)
- **Flexprice API Reference**: [Event Ingestion](https://docs.flexprice.io/api-reference/events/ingest-event)
- **Bento Docs**: [warpstreamlabs.github.io/bento](https://warpstreamlabs.github.io/bento/)
- **Issues**: [GitHub Issues](https://github.com/flexprice/flexprice/issues)

---

## Related

- [Bento](https://github.com/warpstreamlabs/bento) — Open source stream processor (MIT license)
- [Flexprice](https://flexprice.io) — Usage-based billing platform
