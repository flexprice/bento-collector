#!/usr/bin/env bash
# Start a Syncron-like Prometheus (basic auth) loaded with 7 days of usage.
#   PROM_USER / PROM_PASSWORD  credentials Prometheus will require (default syncron / syncron-secret)
#   PROM_PORT                  listen port (default 9095)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="${SANDBOX_DIR:-$here/.sandbox}"
image="prom/prometheus:v3.5.1"
user="${PROM_USER:-syncron}"
pass="${PROM_PASSWORD:-syncron-secret}"
port="${PROM_PORT:-9095}"

rm -rf "$work" && mkdir -p "$work/data"
python3 "$here/fixtures.py" > "$work/metrics.om"
hash="$(python3 -c 'import bcrypt,sys; print(bcrypt.hashpw(sys.argv[1].encode(), bcrypt.gensalt()).decode())' "$pass")"
printf 'basic_auth_users:\n  %s: %s\n' "$user" "$hash" > "$work/web.yml"
echo 'scrape_configs: []' > "$work/prometheus.yml"
if command -v prometheus >/dev/null && command -v promtool >/dev/null; then
  # Local binaries (no Docker Hub pulls).
  promtool tsdb create-blocks-from openmetrics "$work/metrics.om" "$work/data" >/dev/null
  [ -f "$work/../.prometheus.pid" ] && kill "$(cat "$work/../.prometheus.pid")" 2>/dev/null || true
  nohup prometheus --config.file="$work/prometheus.yml" --web.config.file="$work/web.yml" \
    --storage.tsdb.path="$work/data" --storage.tsdb.retention.time=30d \
    --web.listen-address="127.0.0.1:$port" > "$work/prometheus.log" 2>&1 &
  echo $! > "$work/../.prometheus.pid"
else
  uid="$(id -u):$(id -g)"
  docker run --rm --user "$uid" -v "$work:/fixture" --entrypoint /bin/promtool "$image" \
    tsdb create-blocks-from openmetrics /fixture/metrics.om /fixture/data >/dev/null
  docker rm -f syncron-prometheus >/dev/null 2>&1 || true
  docker run -d --name syncron-prometheus --user "$uid" -v "$work:/fixture" -p "127.0.0.1:$port:9090" "$image" \
    --config.file=/fixture/prometheus.yml --web.config.file=/fixture/web.yml \
    --storage.tsdb.path=/fixture/data --storage.tsdb.retention.time=30d >/dev/null
fi
for _ in $(seq 30); do
  if curl -sf -u "$user:$pass" "http://127.0.0.1:$port/api/v1/status/buildinfo" >/dev/null; then
    echo "Syncron Prometheus ready on http://127.0.0.1:$port (user $user)"; exit 0
  fi
  sleep 1
done
echo "Prometheus did not start; see $work/prometheus.log" >&2; exit 1
