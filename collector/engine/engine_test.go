package engine

import (
	"github.com/flexprice/bento-collector/collector/spec"
	"github.com/flexprice/bento-collector/collector/store"

	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
)

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

func newTestEngine(t *testing.T, promURL, fpURL string) (*Engine, spec.Collector) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "data"), false)
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.FileSecrets{Dir: filepath.Join(dir, "secrets")}
	secrets.Put("prom", "prom-token")
	secrets.Put("fp", "fp-key")
	src, dst := fixture.Connections(promURL, fpURL)
	for _, c := range []spec.Connection{src, dst} {
		if _, err := st.SaveConnection(c); err != nil {
			t.Fatal(err)
		}
	}
	c, err := st.SaveCollector(fixture.CPUCollector())
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(st, secrets), c
}

const threeTenants = `[
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_acme","instance":"dsi_a"},"value":[TS,"48"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_globex","instance":"dsi_b"},"value":[TS,"24.5"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_newco","instance":"dsi_c"},"value":[TS,"10"]},
 {"metric":{"label_tags_syncron_com_sy_tenant":"trn_acme"},"value":[TS,"3"]}
]`

func TestRunScenarios(t *testing.T) {
	day := spec.DayWindow(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))

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

func TestCompletenessNaNFails(t *testing.T) {
	fp := &fakeFlexprice{customers: map[string]bool{"cust_acme": true, "cust_globex": true}, meters: map[string]bool{"syncron_cpu_hours": true}}
	fps, prom := httptest.NewServer(fp), fakePrometheus(t, "NaN", threeTenants)
	defer fps.Close()
	defer prom.Close()
	e, c := newTestEngine(t, prom.URL, fps.URL)
	res, err := e.Run(context.Background(), c, spec.DayWindow(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 0 || res.OK {
		t.Fatal("a NaN completeness value must not pass")
	}
}
