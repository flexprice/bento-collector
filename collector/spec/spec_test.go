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
		"bad cron":           func(c *spec.Collector) { c.Trigger.Schedule = "every day" },
		"reserved property":  func(c *spec.Collector) { c.Mapping.Properties = map[string]string{"value": "1"} },
		"static no value":    func(c *spec.Collector) { c.Mapping.Customer = spec.CustomerMapping{From: spec.CustomerStatic} },
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

func TestParseDay(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	w, err := spec.ParseDay("", now)
	if err != nil || w.Start.Format("2006-01-02") != "2026-11-16" {
		t.Fatal(w, err)
	}
	if _, err := spec.ParseDay("2026-11-17", now); err == nil {
		t.Fatal("an unfinished day must be refused")
	}
}

func TestConnectionValidation(t *testing.T) {
	bad := []spec.Connection{
		{ID: "p", Type: spec.ConnectionPrometheus, URL: "http://x", AuthType: "api_key", SecretRef: "p"},
		{ID: "p", Type: spec.ConnectionPrometheus, URL: "http://x", Headers: map[string]string{"Authorization": "Bearer x"}},
		{ID: "f", Type: spec.ConnectionFlexprice, URL: "http://x", AuthType: "api_key", SecretRef: "f", Headers: map[string]string{"X-Api-Key": "k"}},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("expected %+v to be refused", c)
		}
	}
	ok := spec.Connection{ID: "f", Type: spec.ConnectionFlexprice, URL: "http://x", AuthType: "api_key", SecretRef: "f", Headers: map[string]string{"x-environment-id": "env"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
