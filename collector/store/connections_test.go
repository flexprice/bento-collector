package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConnections(t *testing.T) {
	file := filepath.Join(t.TempDir(), "connections.yaml")
	os.WriteFile(file, []byte(`connections:
  - id: syncron-prom
    type: prometheus
    url: ${PROM_URL}
    auth_type: basic
    username: syncron
    secret_env: PROM_PASSWORD
  - id: flexprice
    type: flexprice
    url: https://api.cloud.flexprice.io
    secret_env: FP_KEY
    headers: { x-environment-id: "${FP_ENV}" }
`), 0o600)
	env := map[string]string{"PROM_URL": "http://prom:9090", "PROM_PASSWORD": "pw", "FP_KEY": "sk", "FP_ENV": "env-1"}
	getenv := func(k string) string { return env[k] }

	conns, err := LoadConnections(file, getenv)
	if err != nil {
		t.Fatal(err)
	}
	list := conns.List()
	if len(list) != 2 || list[0].URL != "http://prom:9090" || list[0].SecretEnv != "PROM_PASSWORD" || list[1].Headers["x-environment-id"] != "env-1" {
		t.Fatalf("unexpected connections: %+v", list)
	}
	if v, err := conns.Secret(list[0]); err != nil || v != "pw" {
		t.Fatalf("secret: %q %v", v, err)
	}
	if _, err := conns.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown connection: %v", err)
	}

	delete(env, "FP_KEY")
	delete(env, "PROM_URL")
	if _, err := LoadConnections(file, getenv); err == nil || !strings.Contains(err.Error(), "FP_KEY, PROM_URL") {
		t.Fatalf("missing variables not reported: %v", err)
	}
}
