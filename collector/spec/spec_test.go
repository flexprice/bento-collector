package spec_test

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"testing"
	"time"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
)

func TestValidate(t *testing.T) {
	cases := map[string]func(*spec.Collector){
		"bad id":             func(c *spec.Collector) { c.ID = "Bad ID" },
		"no query":           func(c *spec.Collector) { c.Source.Query = " " },
		"no event name":      func(c *spec.Collector) { c.Mapping.EventName = "" },
		"usage_date no slot": func(c *spec.Collector) { c.Source.Mode = spec.ModeUsageDate },
		"reserved property":  func(c *spec.Collector) { c.Mapping.Properties = map[string]string{"value": "1"} },
		"static no value":    func(c *spec.Collector) { c.Mapping.Customer = spec.CustomerMapping{From: spec.CustomerStatic} },
		"unknown window":     func(c *spec.Collector) { c.Trigger.Window = "5m" },
		"delay not shorter":  func(c *spec.Collector) { c.Trigger.Window, c.Trigger.Delay = "1h", "1h" },
		"delay not minutes":  func(c *spec.Collector) { c.Trigger.Delay = "90s" },
		"usage_date hourly": func(c *spec.Collector) {
			c.Source.Mode, c.Source.Query, c.Trigger.Window, c.Trigger.Delay = spec.ModeUsageDate, "x{usage_date=\"__USAGE_DATE__\"}", "1h", "5m"
		},
		"filter label and fixed": func(c *spec.Collector) {
			c.Mapping.PropertyLabels = map[string]string{"product": "instance"}
		},
		"reserved value prop": func(c *spec.Collector) {
			c.Mapping.ValueProperty = "cores"
			c.Mapping.Properties = map[string]string{"cores": "1"}
		},
	}
	for name, mutate := range cases {
		c := fixture.CPUCollector()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	c := fixture.CPUCollector()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWindows(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	for _, tc := range []struct {
		size, delay time.Duration
		now, start  string
	}{
		{24 * time.Hour, 15 * time.Minute, "2026-11-18T00:15:00Z", "2026-11-17T00:00:00Z"},
		{24 * time.Hour, 15 * time.Minute, "2026-11-18T00:14:59Z", "2026-11-16T00:00:00Z"},
		{time.Hour, 5 * time.Minute, "2026-11-18T13:05:00Z", "2026-11-18T12:00:00Z"},
	} {
		if w := spec.LastClosed(tc.size, tc.delay, at(tc.now)); !w.Start.Equal(at(tc.start)) || w.End.Sub(w.Start) != tc.size {
			t.Errorf("LastClosed(%v, %v, %s) = %v, want start %s", tc.size, tc.delay, tc.now, w, tc.start)
		}
	}

	now := at("2026-11-18T13:20:00Z")
	if w, err := spec.ParseWindow("2026-11-18T12:00", time.Hour, 5*time.Minute, now); err != nil || w.Length() != "1h" {
		t.Fatal(w, err)
	}
	if _, err := spec.ParseWindow("2026-11-18T12:30", time.Hour, 5*time.Minute, now); err == nil {
		t.Fatal("an unaligned window must be refused")
	}
	if _, err := spec.ParseWindow("2026-11-18T13:00", time.Hour, 5*time.Minute, now); err == nil {
		t.Fatal("an unfinished window must be refused")
	}
	if _, err := spec.ParseWindow("2026-11-18", 24*time.Hour, 15*time.Minute, now); err == nil {
		t.Fatal("an unfinished day must be refused")
	}
}

func TestParseRange(t *testing.T) {
	now := time.Date(2026, 11, 19, 4, 20, 0, 0, time.UTC)
	w, err := spec.ParseRange("2026-11-12T13:40", "2026-11-18T09:59", time.Hour, 5*time.Minute, now)
	if err != nil || w.Start.Format(time.RFC3339) != "2026-11-12T13:00:00Z" || w.End.Format(time.RFC3339) != "2026-11-18T09:00:00Z" || w.Length() != "140h" {
		t.Fatal(w, err)
	}
	if w, err := spec.ParseRange("", "", 24*time.Hour, 15*time.Minute, now); err != nil || w.Length() != "7d" || w.End.Format(time.RFC3339) != "2026-11-19T00:00:00Z" {
		t.Fatal(w, err)
	}
	for _, r := range [][2]string{{"2026-11-18T10:00", "2026-11-18T10:30"}, {"2026-11-18", "2026-11-20"}, {"2026-01-01", "2026-11-18"}} {
		if _, err := spec.ParseRange(r[0], r[1], time.Hour, 5*time.Minute, now); err == nil {
			t.Errorf("%v must be refused", r)
		}
	}
}

func TestCron(t *testing.T) {
	for _, tc := range []struct{ window, delay, cron string }{
		{"1h", "5m", "5 * * * *"},
		{"6h", "5m", "5 */6 * * *"},
		{"1d", "15m", "15 0 * * *"},
		{"1d", "150m", "30 2 * * *"},
	} {
		if got := (spec.Trigger{Window: tc.window, Delay: tc.delay}).Cron(); got != tc.cron {
			t.Errorf("%s/%s: cron %q, want %q", tc.window, tc.delay, got, tc.cron)
		}
	}
}

func TestQueryFor(t *testing.T) {
	w := spec.WindowOf(time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC), time.Hour)
	s := spec.Source{Query: `sum(increase(x[__WINDOW__])) + y{d="__USAGE_DATE__"}`}
	want := `sum(increase(x[1h])) + y{d="2026-11-18"}`
	if got := s.QueryFor(w); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestConnectionValidation(t *testing.T) {
	bad := []spec.Connection{
		{ID: "p", Type: "prometheus", URL: "http://x", AuthType: "basic", SecretEnv: "P"},
		{ID: "p", Type: "prometheus", URL: "http://x", Headers: map[string]string{"Authorization": "Bearer x"}},
		{ID: "f", Type: "flexprice", URL: "http://x", AuthType: "api_key", SecretEnv: "F", Headers: map[string]string{"X-Api-Key": "k"}},
		{ID: "f", Type: "flexprice", URL: "http://x", SecretEnv: "not a var"},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("expected %+v to be refused", c)
		}
	}
	ok := spec.Connection{ID: "f", Type: "flexprice", URL: "http://x", AuthType: "api_key", SecretEnv: "F", Headers: map[string]string{"x-environment-id": "env"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
