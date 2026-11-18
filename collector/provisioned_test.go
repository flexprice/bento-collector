package collector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedConnections(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "connections.yaml")
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
	if conns[0].URL != "http://prom:9090" || conns[0].SecretRef != "env:PROM_PASSWORD" || !conns[0].Managed || conns[1].AuthType != "api_key" || conns[1].Headers["x-environment-id"] != "env-1" {
		t.Fatalf("unexpected connections: %+v", conns)
	}

	delete(env, "FP_KEY")
	delete(env, "PROM_URL")
	if _, err := LoadConnections(file, getenv); err == nil || !strings.Contains(err.Error(), "FP_KEY, PROM_URL") {
		t.Fatalf("missing variables not reported: %v", err)
	}
	env["FP_KEY"], env["PROM_URL"] = "sk", "http://prom:9090"

	files, _ := NewFileStore(filepath.Join(dir, "data"), false)
	files.SaveConnection(Connection{ID: "local-prom", Type: ConnectionPrometheus, URL: "http://localhost:9090"})
	store := NewManagedStore(files, conns, false)
	if _, err := store.SaveConnection(Connection{ID: "syncron-prom", Type: ConnectionPrometheus, URL: "http://evil"}); !errors.Is(err, ErrManaged) {
		t.Fatalf("managed connection was editable: %v", err)
	}
	if list, _ := store.ListConnections(); len(list) != 3 {
		t.Fatalf("want managed + UI connections, got %d", len(list))
	}
	store.Locked = true
	if list, _ := store.ListConnections(); len(list) != 2 {
		t.Fatalf("locked store should list only managed connections, got %d", len(list))
	}
	if _, err := store.GetConnection("local-prom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("locked store returned a UI connection: %v", err)
	}
	if _, err := store.SaveConnection(Connection{ID: "new", Type: ConnectionPrometheus, URL: "http://x"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked store accepted a new connection: %v", err)
	}

	secrets := EnvSecrets{Next: FileSecrets{Dir: filepath.Join(dir, "secrets")}, Getenv: getenv}
	if v, err := secrets.Get("env:PROM_PASSWORD"); err != nil || v != "pw" {
		t.Fatalf("env secret: %q %v", v, err)
	}
	if err := secrets.Put("env:PROM_PASSWORD", "x"); !errors.Is(err, ErrManaged) {
		t.Fatalf("env secret was writable: %v", err)
	}

	// A UI-created connection must not be able to read arbitrary environment variables.
	ui := Connection{ID: "steal", Type: ConnectionPrometheus, URL: "http://evil", AuthType: "bearer", SecretRef: "env:AWS_SECRET_ACCESS_KEY"}
	if err := ui.Validate(); err == nil {
		t.Fatal("UI connection accepted an env secret_ref")
	}
}
