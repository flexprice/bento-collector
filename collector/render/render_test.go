package render

import (
	"github.com/flexprice/bento-collector/collector/flexprice"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/source/prometheus"
	"github.com/flexprice/bento-collector/collector/spec"

	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
	"github.com/warpstreamlabs/bento/public/service"

	_ "github.com/flexprice/bento-collector/output"
	_ "github.com/warpstreamlabs/bento/public/components/io"
	_ "github.com/warpstreamlabs/bento/public/components/pure"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestStandaloneGolden(t *testing.T) {
	src, dst := fixture.Connections("https://prometheus.syncron.internal", "https://api.cloud.flexprice.io")
	day, _ := time.Parse("2006-01-02", "2026-11-16")
	in := source.Input{Collector: fixture.CPUCollector(), Conn: src, Secret: "${PROMETHEUS_TOKEN}", Window: spec.DayWindow(day), Now: day}
	out, err := flexprice.Output(dst, "${FLEXPRICE_API_KEY}", in.Collector.ID)
	if err != nil {
		t.Fatal(err)
	}
	for file, scheduled := range map[string]bool{"prometheus-day.yaml": false, "prometheus-scheduled.yaml": true} {
		got, err := Standalone(prometheus.Provider{}, in, out, scheduled)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", file)
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
			t.Fatalf("%s changed; run go test ./collector/render -update and review the diff\n%s", file, got)
		}
		if !strings.Contains(got, "${PROMETHEUS_TOKEN}") || !strings.Contains(got, "${FLEXPRICE_API_KEY}") {
			t.Fatal("exported config must use env placeholders for secrets")
		}
		if scheduled != strings.Contains(got, "TZ=UTC 15 0 * * *") {
			t.Fatalf("%s: schedule not rendered as expected", file)
		}
		// Every rendered config must be a valid Bento config.
		if err := service.NewStreamBuilder().SetYAML(strings.NewReplacer("${PROMETHEUS_TOKEN}", "x", "${FLEXPRICE_API_KEY}", "x").Replace(got)); err != nil {
			t.Fatalf("%s is not a valid Bento config: %v", file, err)
		}
	}
}
