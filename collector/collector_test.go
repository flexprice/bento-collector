package collector

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

func syncronCPU() Collector {
	c := Collector{
		ID:   "syncron-cpu-hours",
		Name: "Syncron CPU core-hours",
		Source: Source{Connection: "prom", Mode: ModeWindow,
			Query: `sum by (label_tags_syncron_com_sy_tenant, instance) (label_replace(sum_over_time(dp_dsi_pod_cpu_requests_cores[1d]), "instance", "$1", "label_tags_syncron_com_dataops_data_service_instance", "(.*)")) / 60`},
		Trigger: Trigger{Completeness: &Completeness{Query: "count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440", Min: 0.999}},
		Mapping: Mapping{
			EventName:      "syncron_cpu_hours",
			Unit:           "core_hours",
			Customer:       CustomerMapping{From: CustomerLabel, Label: "label_tags_syncron_com_sy_tenant", Lookup: map[string]string{"trn_acme": "cust_acme", "trn_globex": "cust_globex"}},
			RequiredLabels: []string{"instance"},
			Properties:     map[string]string{"product": "dataops"},
		},
		Destination: "fp",
		Version:     3,
	}
	c.Normalize()
	return c
}

func connections(promURL, fpURL string) (Connection, Connection) {
	return Connection{ID: "prom", Type: ConnectionPrometheus, URL: promURL, AuthType: "bearer", SecretRef: "prom"},
		Connection{ID: "fp", Type: ConnectionFlexprice, URL: fpURL, AuthType: "api_key", SecretRef: "fp"}
}

func TestRenderConfigGolden(t *testing.T) {
	src, dst := connections("https://prometheus.syncron.internal", "https://api.cloud.flexprice.io")
	day, _ := time.Parse("2006-01-02", "2026-11-16")
	got, err := RenderConfig(RenderInput{Collector: syncronCPU(), Source: src, Destination: dst, Window: DayWindow(day)})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "syncron-cpu-hours.yaml")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("rendered config changed; run go test ./collector -update and review the diff\n%s", got)
	}
	if strings.Contains(got, "secret") || !strings.Contains(got, "${PROMETHEUS_TOKEN}") || !strings.Contains(got, "${FLEXPRICE_API_KEY}") {
		t.Fatal("exported config must use env placeholders for secrets")
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]func(*Collector){
		"bad id":             func(c *Collector) { c.ID = "Bad ID" },
		"no query":           func(c *Collector) { c.Source.Query = " " },
		"no event name":      func(c *Collector) { c.Mapping.EventName = "" },
		"usage_date no slot": func(c *Collector) { c.Source.Mode = ModeUsageDate },
		"bad cron":           func(c *Collector) { c.Trigger.Schedule = "every day" },
		"reserved property":  func(c *Collector) { c.Mapping.Properties = map[string]string{"value": "1"} },
		"static no value":    func(c *Collector) { c.Mapping.Customer = CustomerMapping{From: CustomerStatic} },
	}
	for name, mutate := range cases {
		c := syncronCPU()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	c := syncronCPU()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseDay(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	w, err := ParseDay("", now)
	if err != nil || w.Start.Format("2006-01-02") != "2026-11-16" {
		t.Fatal(w, err)
	}
	if _, err := ParseDay("2026-11-17", now); err == nil {
		t.Fatal("an unfinished day must be refused")
	}
}

type fakeFlexprice struct {
	mu        sync.Mutex
	customers map[string]bool
	meters    map[string]bool
	received  []map[string]any
}

func (f *fakeFlexprice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("x-api-key") != "fp-key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/customers/external/"):
		if !f.customers[strings.TrimPrefix(r.URL.Path, "/v1/customers/external/")] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{}`))
	case r.URL.Path == "/v1/meters":
		name := r.URL.Query().Get("event_name")
		if f.meters[name] {
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "m1", "event_name": name, "status": "published"}}})
			return
		}
		w.Write([]byte(`{"items":[]}`))
	case r.URL.Path == "/v1/events/bulk":
		var body struct {
			Events []map[string]any `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.received = append(f.received, body.Events...)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"message":"ok"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// fakePrometheus answers the collector query and the completeness query.
func fakePrometheus(t *testing.T, completeness string, series string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer prom-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.ParseForm()
		ts, _ := time.Parse(time.RFC3339, r.Form.Get("time"))
		q := r.Form.Get("query")
		var result string
		if strings.Contains(q, "dp_pod_reservation_rule_evaluated") {
			result = `[{"metric":{},"value":[` + itoa(ts.Unix()) + `,"` + completeness + `"]}]`
		} else {
			result = strings.ReplaceAll(series, "TS", itoa(ts.Unix()))
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":` + result + `}}`))
	}))
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func newTestEngine(t *testing.T, promURL, fpURL string) (*Engine, Collector) {
	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, "data"), false)
	if err != nil {
		t.Fatal(err)
	}
	secrets := FileSecrets{Dir: filepath.Join(dir, "secrets")}
	secrets.Put("prom", "prom-token")
	secrets.Put("fp", "fp-key")
	src, dst := connections(promURL, fpURL)
	for _, c := range []Connection{src, dst} {
		if _, err := store.SaveConnection(c); err != nil {
			t.Fatal(err)
		}
	}
	c, err := store.SaveCollector(syncronCPU())
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(store, secrets), c
}

const threeTenants = `[
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_acme","instance":"dsi_a"},"value":[TS,"48"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_globex","instance":"dsi_b"},"value":[TS,"24.5"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_newco","instance":"dsi_c"},"value":[TS,"10"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_acme"},"value":[TS,"3"]}
]`

func TestRunScenarios(t *testing.T) {
	day := DayWindow(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))

	t.Run("sends valid rows and rejects the rest", func(t *testing.T) {
		fp := &fakeFlexprice{customers: map[string]bool{"cust_acme": true, "cust_globex": true}, meters: map[string]bool{"syncron_cpu_hours": true}}
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, "1", threeTenants)
		defer fps.Close()
		defer prom.Close()
		e, c := newTestEngine(t, prom.URL, fps.URL)
		res, err := e.Run(context.Background(), c, day, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Sent != 2 || res.Rejected != 2 || res.OK {
			t.Fatalf("sent=%d rejected=%d ok=%v checks=%v", res.Sent, res.Rejected, res.OK, res.Checks)
		}
		ev := fp.received[0]
		if ev["timestamp"] != "2026-11-16T00:00:00Z" || ev["external_customer_id"] != "cust_acme" {
			t.Fatalf("unexpected event %v", ev)
		}
		props := ev["properties"].(map[string]any)
		if props["value"] != "48" || props["unit"] != "core_hours" || props["product"] != "dataops" || props["instance"] != "dsi_a" {
			t.Fatalf("unexpected properties %v", props)
		}
		again, _ := e.Run(context.Background(), c, day, false)
		_ = again
		if fp.received[2]["event_id"] != fp.received[0]["event_id"] {
			t.Fatal("a re-run must produce the same event_id")
		}
	})

	t.Run("skipped tenants are counted, not rejected", func(t *testing.T) {
		fp := &fakeFlexprice{customers: map[string]bool{"cust_acme": true, "cust_globex": true}, meters: map[string]bool{"syncron_cpu_hours": true}}
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, "1", threeTenants)
		defer fps.Close()
		defer prom.Close()
		e, c := newTestEngine(t, prom.URL, fps.URL)
		c.Mapping.Customer.Skip = []string{"trn_newco"}
		res, err := e.Run(context.Background(), c, day, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Skipped != 1 || res.Rejected != 1 || res.Sent != 2 {
			t.Fatalf("skipped=%d rejected=%d sent=%d", res.Skipped, res.Rejected, res.Sent)
		}
	})

	t.Run("incomplete day sends nothing", func(t *testing.T) {
		fp := &fakeFlexprice{customers: map[string]bool{"cust_acme": true, "cust_globex": true}, meters: map[string]bool{"syncron_cpu_hours": true}}
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, "0.95", threeTenants)
		defer fps.Close()
		defer prom.Close()
		e, c := newTestEngine(t, prom.URL, fps.URL)
		res, err := e.Run(context.Background(), c, day, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Sent != 0 || res.OK || len(fp.received) != 0 {
			t.Fatalf("incomplete day must not send: %+v", res.Checks)
		}
	})

	t.Run("missing meter sends nothing", func(t *testing.T) {
		fp := &fakeFlexprice{customers: map[string]bool{"cust_acme": true, "cust_globex": true}, meters: map[string]bool{}}
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, "1", threeTenants)
		defer fps.Close()
		defer prom.Close()
		e, c := newTestEngine(t, prom.URL, fps.URL)
		res, err := e.Run(context.Background(), c, day, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Sent != 0 || len(fp.received) != 0 {
			t.Fatal("missing meter must not send")
		}
	})

	t.Run("preview never sends", func(t *testing.T) {
		fp := &fakeFlexprice{customers: map[string]bool{"cust_acme": true, "cust_globex": true}, meters: map[string]bool{"syncron_cpu_hours": true}}
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, "1", threeTenants)
		defer fps.Close()
		defer prom.Close()
		e, c := newTestEngine(t, prom.URL, fps.URL)
		res, err := e.Preview(context.Background(), c, day)
		if err != nil {
			t.Fatal(err)
		}
		if res.Events != 2 || len(fp.received) != 0 {
			t.Fatalf("events=%d received=%d", res.Events, len(fp.received))
		}
	})

	t.Run("bad prometheus credentials fail the run", func(t *testing.T) {
		fp := &fakeFlexprice{customers: map[string]bool{}, meters: map[string]bool{"syncron_cpu_hours": true}}
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, "1", threeTenants)
		defer fps.Close()
		defer prom.Close()
		e, c := newTestEngine(t, prom.URL, fps.URL)
		e.Secrets.Put("prom", "wrong")
		res, err := e.Run(context.Background(), c, day, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.OK || res.Sent != 0 {
			t.Fatalf("expected failure, got %+v", res.Checks)
		}
	})
}

func TestStoreVersions(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	c := syncronCPU()
	for i := 0; i < 3; i++ {
		c.Mapping.Unit = "u" + itoa(int64(i))
		if _, err := store.SaveCollector(c); err != nil {
			t.Fatal(err)
		}
	}
	latest, _ := store.GetCollector(c.ID, 0)
	first, _ := store.GetCollector(c.ID, 1)
	if latest.Version != 3 || first.Mapping.Unit != "u0" {
		t.Fatalf("latest=%d first=%s", latest.Version, first.Mapping.Unit)
	}
	if _, err := store.GetCollector("../etc", 0); err == nil {
		t.Fatal("path traversal must be refused")
	}
}
