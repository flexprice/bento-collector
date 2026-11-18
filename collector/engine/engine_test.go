package engine

import (
	"github.com/flexprice/bento-collector/collector/spec"
	"github.com/flexprice/bento-collector/collector/store"

	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/source/prometheus"
)

var testSecrets = map[string]string{"PROMETHEUS_TOKEN": "prom-token", "FLEXPRICE_API_KEY": "fp-key"}

type fakeFlexprice struct {
	mu        sync.Mutex
	customers map[string]bool
	meters    map[string]bool
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

func newTestEngine(t *testing.T, promURL, fpURL string) (*Engine, spec.Collector) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "data"), filepath.Join(dir, "streams"), false)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "connections.yaml")
	if err := os.WriteFile(file, []byte(fixture.ConnectionsFile(promURL, fpURL)), 0o600); err != nil {
		t.Fatal(err)
	}
	conns, err := store.LoadConnections(file, func(k string) string { return testSecrets[k] })
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.SaveCollector(fixture.CPUCollector())
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(st, conns, source.NewRegistry(prometheus.Provider{}))
	if err != nil {
		t.Fatal(err)
	}
	return e, c
}

const threeTenants = `[
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_acme","instance":"dsi_a"},"value":[TS,"48"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_globex","instance":"dsi_b"},"value":[TS,"24.5"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_newco","instance":"dsi_c"},"value":[TS,"10"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_acme"},"value":[TS,"3"]}
]`

func TestPreviewScenarios(t *testing.T) {
	day := spec.DayWindow(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	known := map[string]bool{"cust_acme": true, "cust_globex": true}
	meter := map[string]bool{"syncron_cpu_hours": true}

	preview := func(t *testing.T, fp *fakeFlexprice, completeness string, edit func(*spec.Collector)) *Result {
		t.Helper()
		fps, prom := httptest.NewServer(fp), fakePrometheus(t, completeness, threeTenants)
		t.Cleanup(fps.Close)
		t.Cleanup(prom.Close)
		e, c := newTestEngine(t, prom.URL, fps.URL)
		if edit != nil {
			edit(&c)
		}
		res, err := e.Preview(context.Background(), c, day)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	events := func(res *Result) []map[string]any {
		var out []map[string]any
		for _, r := range res.Rows {
			if r.Error == "" {
				out = append(out, r.Event)
			}
		}
		return out
	}

	t.Run("maps valid rows and rejects the rest", func(t *testing.T) {
		res := preview(t, &fakeFlexprice{customers: known, meters: meter}, "1", nil)
		if res.Events != 2 || res.Rejected != 2 || res.OK || !res.Completeness.OK {
			t.Fatalf("events=%d rejected=%d ok=%v checks=%v", res.Events, res.Rejected, res.OK, res.Checks)
		}
		ev := events(res)[0]
		if ev["timestamp"] != "2026-11-16T00:00:00Z" || ev["external_customer_id"] != "cust_acme" {
			t.Fatalf("unexpected event %v", ev)
		}
		props := ev["properties"].(map[string]any)
		if props["value"] != "48" || props["unit"] != "core_hours" || props["product"] != "dataops" || props["instance"] != "dsi_a" {
			t.Fatalf("unexpected properties %v", props)
		}
		again := preview(t, &fakeFlexprice{customers: known, meters: meter}, "1", nil)
		if events(again)[0]["event_id"] != ev["event_id"] {
			t.Fatal("the same day must produce the same event_id")
		}
	})

	t.Run("skipped tenants are counted, not rejected", func(t *testing.T) {
		res := preview(t, &fakeFlexprice{customers: known, meters: meter}, "1", func(c *spec.Collector) { c.Mapping.Customer.Skip = []string{"trn_newco"} })
		if res.Skipped != 1 || res.Rejected != 1 || res.Events != 2 {
			t.Fatalf("skipped=%d rejected=%d events=%d", res.Skipped, res.Rejected, res.Events)
		}
	})

	t.Run("incomplete day is blocked by the rendered gate", func(t *testing.T) {
		for _, v := range []string{"0.95", "NaN"} {
			res := preview(t, &fakeFlexprice{customers: known, meters: meter}, v, nil)
			if res.OK || res.Completeness.OK {
				t.Fatalf("completeness %s must not pass: %+v", v, res.Completeness)
			}
		}
	})

	t.Run("missing meter blocks", func(t *testing.T) {
		if res := preview(t, &fakeFlexprice{customers: known, meters: map[string]bool{}}, "1", nil); res.OK {
			t.Fatal("missing meter must block")
		}
	})

	t.Run("bad prometheus credentials fail", func(t *testing.T) {
		testSecrets["PROMETHEUS_TOKEN"] = "wrong"
		defer func() { testSecrets["PROMETHEUS_TOKEN"] = "prom-token" }()
		res := preview(t, &fakeFlexprice{customers: map[string]bool{}, meters: meter}, "1", nil)
		if res.OK || res.Completeness.Error == "" {
			t.Fatalf("expected failure, got %+v", res.Checks)
		}
	})
}

func TestPublishChecks(t *testing.T) {
	prom := fakePrometheus(t, "1", threeTenants)
	defer prom.Close()
	for name, tc := range map[string]struct {
		customers, meters map[string]bool
		blocked           bool
	}{
		"all customers and the meter exist": {map[string]bool{"cust_acme": true, "cust_globex": true}, map[string]bool{"syncron_cpu_hours": true}, false},
		"a mapped customer is missing":      {map[string]bool{"cust_acme": true}, map[string]bool{"syncron_cpu_hours": true}, true},
		"no meter":                          {map[string]bool{"cust_acme": true, "cust_globex": true}, map[string]bool{}, true},
	} {
		t.Run(name, func(t *testing.T) {
			fps := httptest.NewServer(&fakeFlexprice{customers: tc.customers, meters: tc.meters})
			defer fps.Close()
			e, c := newTestEngine(t, prom.URL, fps.URL)
			y, checks, err := e.Publish(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if Blocked(checks) != tc.blocked {
				t.Fatalf("blocked=%v checks=%v", Blocked(checks), checks)
			}
			if !strings.Contains(y, "TZ=UTC 15 0 * * *") || !strings.Contains(y, "${FLEXPRICE_API_KEY}") {
				t.Fatalf("publish must render the scheduled config with env placeholders:\n%s", y)
			}
		})
	}
}
