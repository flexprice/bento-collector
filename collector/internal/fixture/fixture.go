// Package fixture holds the sample collector shared by the collector tests.
package fixture

import (
	"github.com/flexprice/bento-collector/collector/spec"
)

// CPUCollector is a daily CPU core-hours collector with a tenant → customer table.
func CPUCollector() spec.Collector {
	c := spec.Collector{
		ID:   "syncron-cpu-hours",
		Name: "Syncron CPU core-hours",
		Source: spec.Source{Type: "prometheus", Connection: "prom", Mode: spec.ModeWindow,
			Query: `sum by (label_tags_syncron_com_sy_tenant, instance) (label_replace(sum_over_time(dp_dsi_pod_cpu_requests_cores[1d]), "instance", "$1", "label_tags_syncron_com_dataops_data_service_instance", "(.*)")) / 60`},
		Trigger: spec.Trigger{Completeness: &spec.Completeness{Query: "count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440", Min: 0.999}},
		Mapping: spec.Mapping{
			EventName:      "syncron_cpu_hours",
			Unit:           "core_hours",
			Customer:       spec.CustomerMapping{From: spec.CustomerLabel, Label: "label_tags_syncron_com_sy_tenant", Lookup: map[string]string{"trn_acme": "cust_acme", "trn_globex": "cust_globex"}},
			RequiredLabels: []string{"instance"},
			Properties:     map[string]string{"product": "dataops"},
		},
		Destination: "fp",
		Version:     3,
	}
	c.Normalize()
	return c
}

// Connections returns a bearer-auth Prometheus and a Flexprice connection,
// with secrets in PROMETHEUS_TOKEN and FLEXPRICE_API_KEY.
func Connections(promURL, fpURL string) (spec.Connection, spec.Connection) {
	return spec.Connection{ID: "prom", Type: "prometheus", URL: promURL, AuthType: "bearer", SecretEnv: "PROMETHEUS_TOKEN"},
		spec.Connection{ID: "fp", Type: "flexprice", URL: fpURL, AuthType: "api_key", SecretEnv: "FLEXPRICE_API_KEY"}
}

// ConnectionsFile is a connections file for the two connections above.
func ConnectionsFile(promURL, fpURL string) string {
	return `connections:
  - { id: prom, type: prometheus, url: "` + promURL + `", auth_type: bearer, secret_env: PROMETHEUS_TOKEN }
  - { id: fp, type: flexprice, url: "` + fpURL + `", secret_env: FLEXPRICE_API_KEY }
`
}
