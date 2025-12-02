# Flexprice Event Collector

Stream events from **Kafka**, **Postgres**, **HTTP**, or 200+ sources to Flexprice for usage-based billing.

> Built on [Bento](https://github.com/warpstreamlabs/bento) - Production-ready stream processor (MIT license)

---

## Quick Start

### 1. Download Binary

```bash
# Linux
curl -L https://github.com/flexprice/flexprice-collector/releases/latest/download/bento-flexprice-linux -o bento-flexprice
chmod +x bento-flexprice

# macOS
curl -L https://github.com/flexprice/flexprice-collector/releases/latest/download/bento-flexprice-macos -o bento-flexprice
chmod +x bento-flexprice
```

### 2. Setup Flexprice (One Time)

In your Flexprice dashboard:
1. **Create Meter** - Name must match `event_name` (e.g., `api_call`)
2. **Import Customers** - Note their `external_customer_id`
3. **Generate API Key** - Settings → API Keys

### 3. Create Config File

**config.yaml:**
```yaml
input:
  kafka:
    addresses: ["${KAFKA_BROKERS}"]
    topics: ["usage-events"]
    consumer_group: flexprice-collector
    start_from_oldest: false
    
    tls:
      enabled: true
    sasl:
      mechanism: PLAIN
      user: "${KAFKA_SASL_USER}"
      password: "${KAFKA_SASL_PASSWORD}"

pipeline:
  processors:
    - mapping: |
        # Required: Must match meter name in Flexprice
        root.event_name = "api_call"
        
        # Required: Must exist in Flexprice customers
        root.external_customer_id = this.customer_id
        
        # Optional: Convert numbers to strings
        root.properties = {
          "duration": this.duration.string(),
          "model": this.model
        }
        
        # Optional: Defaults to now() if not provided
        root.timestamp = this.timestamp.or(now().format_timestamp("2006-01-02T15:04:05Z07:00"))

output:
  flexprice:
    api_host: ${FLEXPRICE_API_HOST}
    api_key: ${FLEXPRICE_API_KEY}
    scheme: https
    
    # Bulk batching (10-100x faster)
    batching:
      count: 100
      period: 5s
    
    max_in_flight: 10

logger:
  level: INFO
```

### 4. Set Environment Variables

```bash
cat > .env << 'EOF'
export FLEXPRICE_API_HOST=api.cloud.flexprice.io
export FLEXPRICE_API_KEY=fp_live_xxxxx
export KAFKA_BROKERS=your-broker.confluent.cloud:9092
export KAFKA_SASL_USER=your_kafka_key
export KAFKA_SASL_PASSWORD=your_kafka_secret
EOF

source .env
```

### 5. Run

```bash
./bento-flexprice -c config.yaml
```

**Expected output:**
```
INFO Flexprice output connected and ready
INFO 📦 Sending bulk batch: 100 events
INFO ✅ Bulk batch accepted successfully
```

---

## Testing Your Setup

### Build Test Tool

```bash
cd examples
go build -o ../send-events send-events.go
cd ..
```

### Run Test

**Terminal 1 - Start consumer:**
```bash
source .env
./bento-flexprice -c examples/kafka-test-flexprice.yaml
```

**Terminal 2 - Send events:**
```bash
source .env
./send-events 50
```

**Verify:** Check Flexprice dashboard → Events page for your events.

---

## Production Deployment

### Docker

```dockerfile
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY bento-flexprice /usr/local/bin/
COPY config.yaml /etc/bento/
CMD ["bento-flexprice", "-c", "/etc/bento/config.yaml"]
```

```bash
docker run -d \
  --name flexprice-collector \
  --restart unless-stopped \
  -e FLEXPRICE_API_HOST=api.cloud.flexprice.io \
  -e FLEXPRICE_API_KEY=fp_live_xxx \
  -e KAFKA_BROKERS=your-kafka:9092 \
  -e KAFKA_SASL_USER=xxx \
  -e KAFKA_SASL_PASSWORD=xxx \
  flexprice-collector:latest
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: flexprice-collector
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: bento
        image: flexprice-collector:latest
        resources:
          requests:
            memory: "256Mi"
            cpu: "200m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        env:
        - name: FLEXPRICE_API_KEY
          valueFrom:
            secretKeyRef:
              name: flexprice-secrets
              key: api-key
        livenessProbe:
          httpGet:
            path: /ping
            port: 4195
```

---

## Event Format

Your events are transformed to Flexprice format:

```json
{
  "event_name": "api_call",           // Required: Must match meter name
  "external_customer_id": "cust_123", // Required: Must exist in Flexprice
  "properties": {
    "duration": "120"                 // Numbers as strings
  },
  "timestamp": "2025-12-02T10:00:00Z" // Optional: ISO 8601
}
```

**⚠️ Important:** Convert numeric properties to strings: `.string()`

---

## Monitoring

### Health Checks

```bash
curl http://localhost:4195/ping    # Liveness
curl http://localhost:4195/metrics # Prometheus metrics
```

### Key Metrics

```
bento_input_received_total     # Events received
bento_output_sent_total        # Events sent to Flexprice
bento_output_batch_sent_total  # Bulk batches sent
bento_output_error_total       # Failed sends
```

---

## Troubleshooting

### Events not appearing in Flexprice?

1. **Check event name matches meter:**
   ```bash
   # View logs
   docker logs flexprice-collector | grep event_name
   ```

2. **Verify customer exists:**
   - Check `external_customer_id` in logs
   - Confirm customer in Flexprice dashboard

3. **Test API key:**
   ```bash
   curl -H "x-api-key: $FLEXPRICE_API_KEY" \
        https://api.cloud.flexprice.io/v1/health
   ```

### Kafka connection failed?

```bash
# Verify credentials
echo $KAFKA_BROKERS
echo $KAFKA_SASL_USER

# Check TLS/SASL settings match your cluster
```

### API error 400?

- **Numeric properties:** Add `.string()` conversion
  ```yaml
  root.properties = {
    "count": this.count.string()  # Not just this.count
  }
  ```

### High memory usage?

- Reduce batch size: `count: 50`
- Limit fetch buffer: `fetch_buffer_cap: 128`
- Scale horizontally: Add more replicas

---

## Other Data Sources

### Postgres

```yaml
input:
  sql_select:
    driver: postgres
    dsn: "postgres://user:pass@host/db"
    table: "events"
    where: "created_at > $1"
```

### HTTP Webhook

```yaml
input:
  http_server:
    address: "0.0.0.0:8080"
    path: "/webhook"
```

### AWS Kinesis

```yaml
input:
  aws_kinesis:
    streams: [usage-events]
```

[See all 200+ inputs →](https://warpstreamlabs.github.io/bento/docs/components/inputs/about)

---

## Performance

- **Throughput:** 10,000+ events/sec per instance
- **Latency:** 50-500ms (depends on batch size)
- **Scaling:** Horizontal (linear)

| Events/sec | CPU | Memory | Replicas |
|------------|-----|--------|----------|
| <1,000 | 100m | 128Mi | 1 |
| 1,000-10,000 | 200m | 256Mi | 2 |
| >10,000 | 500m | 512Mi | 3-5 |

---

## Production Checklist

- [ ] Created meter in Flexprice (matching `event_name`)
- [ ] Imported customers with correct `external_customer_id`
- [ ] Generated production API key
- [ ] Tested locally with sample events
- [ ] Set up monitoring (metrics endpoint)
- [ ] Configured health checks
- [ ] Verified events in Flexprice dashboard

---

## Support

- **Docs:** [docs.flexprice.io](https://docs.flexprice.io)
- **Bento Docs:** [warpstreamlabs.github.io/bento](https://warpstreamlabs.github.io/bento)

