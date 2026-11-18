"""Create the Syncron customers and meters in a Flexprice environment.

  FLEXPRICE_URL      default http://localhost:8080
  FLEXPRICE_API_KEY  required
  FLEXPRICE_ENV_ID   optional x-environment-id header (self-hosted Flexprice)

newco is intentionally not created, so its rows are rejected as unknown.
"""

import json
import os
import urllib.error
import urllib.request

URL = os.environ.get("FLEXPRICE_URL", "http://localhost:8080").rstrip("/")
HEADERS = {"x-api-key": os.environ["FLEXPRICE_API_KEY"], "Content-Type": "application/json"}
if os.environ.get("FLEXPRICE_ENV_ID"):
    HEADERS["x-environment-id"] = os.environ["FLEXPRICE_ENV_ID"]

CUSTOMERS = [("cust_acme", "Acme Corp"), ("cust_globex", "Globex"), ("cust_initech", "Initech")]
METERS = [("syncron_cpu_hours", "Syncron reserved CPU core-hours"),
          ("syncron_memory_gb_hours", "Syncron reserved memory GB-hours"),
          ("syncron_spark_job_count", "Syncron Spark jobs")]


def call(method, path, body=None):
    req = urllib.request.Request(URL + path, method=method, headers=HEADERS,
                                 data=json.dumps(body).encode() if body is not None else None)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


for external_id, name in CUSTOMERS:
    status, _ = call("GET", f"/v1/customers/external/{external_id}")
    if status == 404:
        status, body = call("POST", "/v1/customers", {"external_id": external_id, "name": name})
        assert status < 300, body
    print("customer", external_id, "ok")

existing = {m["event_name"] for m in call("GET", "/v1/meters?limit=500")[1].get("items", [])}
for event_name, name in METERS:
    if event_name not in existing:
        status, body = call("POST", "/v1/meters", {"name": name, "event_name": event_name,
                                                   "aggregation": {"type": "SUM", "field": "value"},
                                                   "reset_usage": "BILLING_PERIOD"})
        assert status < 300, body
    print("meter", event_name, "ok")
