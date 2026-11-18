package render

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderConfigGolden(t *testing.T) {
	src, dst := fixture.Connections("https://prometheus.syncron.internal", "https://api.cloud.flexprice.io")
	day, _ := time.Parse("2006-01-02", "2026-11-16")
	got, err := RenderConfig(RenderInput{Collector: fixture.CPUCollector(), Source: src, Destination: dst, Window: spec.DayWindow(day)})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "prometheus-daily.yaml")
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
		t.Fatalf("rendered config changed; run go test ./collector/render -update and review the diff\n%s", got)
	}
	if strings.Contains(got, "secret") || !strings.Contains(got, "${PROMETHEUS_TOKEN}") || !strings.Contains(got, "${FLEXPRICE_API_KEY}") {
		t.Fatal("exported config must use env placeholders for secrets")
	}
}
