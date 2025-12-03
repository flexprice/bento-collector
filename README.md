# Bento Flexprice Collector

A custom [Bento](https://github.com/warpstreamlabs/bento) distribution that enables streaming usage events to [Flexprice](https://flexprice.io) from **any data source** — Kafka, databases, HTTP APIs, files, and [200+ more connectors](https://warpstreamlabs.github.io/bento/docs/components/inputs/about).

Built on the official Flexprice Go SDK, this collector handles event transformation, batching, retries, and dead-letter queues out of the box.

> **Open Source**: Uses [Bento](https://github.com/warpstreamlabs/bento) (MIT license) — no vendor lock-in.

## Features

- **Flexprice Output Plugin** — Uses official Flexprice Go SDK for reliable event ingestion
- **Any Input Source** — Kafka, PostgreSQL, HTTP, S3, and 200+ connectors
- **Bloblang Transforms** — Transform and enrich events on-the-fly
- **Built-in Reliability** — Retry logic, batching, and dead-letter queue support
- **Docker Ready** — Production-ready container included

---

## Quick Start

### 1. Build the Binary

```bash
go build -o bento-flexprice main.go
```

### 2. Set Environment Variables

Copy the example environment file and configure your Flexprice credentials:

```bash
cp env.example .env
```

Edit `.env` with your Flexprice API credentials (see [env.example](env.example)):

```bash
# Required for Quick Start
FLEXPRICE_API_HOST=api.cloud.flexprice.io
FLEXPRICE_API_KEY=your_api_key_here
```

Then load the environment:

```bash
source .env
```

### 3. Run an Example

**Example 1: Generate Dummy Events → Flexprice**

The simplest way to test — generates random events and sends them directly to Flexprice:

```bash
./bento-flexprice -c examples/dummy-events-to-flexprice.yaml
```

**Example 2: Kafka → Flexprice (with Dead Letter Queue)**

Production-ready example that consumes from Kafka, transforms events, and sends to Flexprice with automatic retries and DLQ for failed events:

```bash
# First, generate test events to Kafka
./bento-flexprice -c examples/kafka/generate-to-kafka.yaml

# Then consume and send to Flexprice (with DLQ support)
./bento-flexprice -c examples/kafka/consume-from-kafka-with-dlq.yaml
```

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

## Examples

| Example | Description | Command |
|---------|-------------|---------|
| [dummy-events-to-flexprice.yaml](examples/dummy-events-to-flexprice.yaml) | Generate dummy events → Flexprice | `./bento-flexprice -c examples/dummy-events-to-flexprice.yaml` |
| [generate-to-kafka.yaml](examples/kafka/generate-to-kafka.yaml) | Generate events → Kafka | `./bento-flexprice -c examples/kafka/generate-to-kafka.yaml` |
| [consume-from-kafka.yaml](examples/kafka/consume-from-kafka.yaml) | Kafka → Flexprice (simple) | `./bento-flexprice -c examples/kafka/consume-from-kafka.yaml` |
| [consume-from-kafka-with-dlq.yaml](examples/kafka/consume-from-kafka-with-dlq.yaml) | Kafka → Flexprice (with DLQ) | `./bento-flexprice -c examples/kafka/consume-from-kafka-with-dlq.yaml` |

### Kafka Testing Flow

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
├── main.go                              # Entry point
├── output/
│   └── flexprice.go                     # Custom Flexprice output plugin
├── examples/
│   ├── dummy-events-to-flexprice.yaml   # Direct: Generate → Flexprice
│   └── kafka/
│       ├── generate-to-kafka.yaml       # Generate → Kafka
│       ├── consume-from-kafka.yaml      # Kafka → Flexprice
│       └── consume-from-kafka-with-dlq.yaml  # Kafka → Flexprice (with DLQ)
├── Dockerfile                           # Production container
├── env.example                          # Environment template
└── README.md
```

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

| Variable | Description | Example |
|----------|-------------|---------|
| `FLEXPRICE_API_HOST` | Flexprice API host | `api.cloud.flexprice.io` |
| `FLEXPRICE_API_KEY` | API key | `fp_xxx` |
| `FLEXPRICE_KAFKA_BROKERS` | Kafka brokers | `pkc-xxx.confluent.cloud:9092` |
| `FLEXPRICE_KAFKA_TOPIC` | Kafka topic | `events` |
| `FLEXPRICE_KAFKA_SASL_USER` | SASL username | From Confluent Cloud |
| `FLEXPRICE_KAFKA_SASL_PASSWORD` | SASL password | From Confluent Cloud |
| `FLEXPRICE_KAFKA_CONSUMER_GROUP` | Consumer group | `bento-flexprice-v1` |
| `FLEXPRICE_KAFKA_DLQ_TOPIC` | Dead letter queue topic | `events-dlq` |

See [env.example](env.example) for the complete list.

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
