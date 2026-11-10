package main

import (
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/warpstreamlabs/bento/public/bloblang"
	"github.com/warpstreamlabs/bento/public/service"
	"gopkg.in/yaml.v3"
)

func TestPrometheusQueryBody(t *testing.T) {
	data, err := os.ReadFile("examples/prometheus-to-flexprice.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Input struct {
			Generate struct {
				Mapping string `yaml:"mapping"`
			} `yaml:"generate"`
		} `yaml:"input"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	mapping, err := bloblang.Parse(config.Input.Generate.Mapping)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixedTime := range []string{"", "1794302460"} {
		t.Run("time="+fixedTime, func(t *testing.T) {
			query := `sum(increase(node_cpu_seconds_total{mode!="idle",region="a+b&c"}[1m])) by (customer_id)`
			t.Setenv("PROMETHEUS_QUERY", query)
			t.Setenv("PROMETHEUS_QUERY_TIME", fixedTime)
			t.Setenv("PROMETHEUS_WINDOW_SECONDS", "60")
			t.Setenv("PROMETHEUS_QUERY_OFFSET_SECONDS", "60")
			message, err := service.NewMessage(nil).BloblangQuery(mapping)
			if err != nil {
				t.Fatal(err)
			}
			body, err := message.AsBytes()
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatal(err)
			}
			if form.Get("query") != query {
				t.Fatalf("query changed in transit: %q", form.Get("query"))
			}
			end, err := time.Parse(time.RFC3339, form.Get("time"))
			if err != nil {
				t.Fatalf("evaluation time must be RFC3339, not a scientific-notation float: %v", err)
			}
			if end.Unix()%60 != 0 {
				t.Fatalf("window is not minute-aligned: %s", end)
			}
			if fixedTime != "" && end.Unix() != 1794302460 {
				t.Fatalf("fixed replay time changed: %s", end)
			}
		})
	}
}
