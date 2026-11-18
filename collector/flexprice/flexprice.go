// Package flexprice is the collector's destination: the API client used for
// checks and sending, and the Bento output for exported configs.
package flexprice

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"fmt"
	"net/url"
)

const Type = "flexprice"

func ValidateConnection(c spec.Connection) error {
	if c.Type != Type {
		return fmt.Errorf("connection %q is a %s connection, not %s", c.ID, c.Type, Type)
	}
	if c.AuthType != "" && c.AuthType != "api_key" {
		return fmt.Errorf("a Flexprice connection uses auth_type api_key")
	}
	if c.SecretEnv == "" {
		return fmt.Errorf("a Flexprice connection needs secret_env for its API key")
	}
	return nil
}

// Output is the Bento flexprice output for an exported config. Rejected rows
// are logged and skipped rows dropped.
func Output(c spec.Connection, apiKey, collectorID string) (map[string]any, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, fmt.Errorf("destination url: %w", err)
	}
	fp := map[string]any{
		"api_host": u.Host,
		"scheme":   u.Scheme,
		"api_key":  apiKey,
		"batching": map[string]any{"count": 1000, "period": "1s"},
	}
	if len(c.Headers) > 0 {
		fp["headers"] = c.Headers
	}
	return map[string]any{"switch": map[string]any{"cases": []any{
		map[string]any{"check": "errored()", "output": map[string]any{"reject": "collector " + collectorID + " rejected a row: ${! error() }"}},
		map[string]any{"check": `@collector_skip == "true"`, "output": map[string]any{"drop": map[string]any{}}},
		map[string]any{"output": map[string]any{"flexprice": fp}},
	}}}, nil
}
