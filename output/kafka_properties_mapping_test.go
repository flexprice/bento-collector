package output

import (
	"os"
	"strings"
	"testing"

	"github.com/warpstreamlabs/bento/public/bloblang"
	"gopkg.in/yaml.v3"

	_ "github.com/warpstreamlabs/bento/public/components/all"
)

const kafkaConfigPath = "../internal/aws-kafka-to-flexprice.yaml"

// extractKafkaPropertiesMapping pulls the properties-stringify processor out of
// the shipped config so this test and the config cannot drift apart.
func extractKafkaPropertiesMapping(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(kafkaConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Pipeline struct {
			Processors []map[string]any `yaml:"processors"`
		} `yaml:"pipeline"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config yaml: %v", err)
	}
	for _, p := range cfg.Pipeline.Processors {
		if m, ok := p["mapping"].(string); ok && strings.Contains(m, ".value.string()") {
			return m
		}
	}
	t.Fatal("could not find the properties-stringify mapping in the config")
	return ""
}

func loadKafkaMapping(t *testing.T) *bloblang.Executor {
	t.Helper()
	exec, err := bloblang.GlobalEnvironment().Parse(extractKafkaPropertiesMapping(t))
	if err != nil {
		t.Fatalf("parse mapping: %v", err)
	}
	return exec
}

func queryKafkaMapping(t *testing.T, exec *bloblang.Executor, in any) map[string]any {
	t.Helper()
	out, err := exec.Query(in)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("mapping output is %T, want map", out)
	}
	return m
}

// TestKafkaPropertiesStringified is the poison-pill regression: a number or bool
// under properties must come out as a string, or json.Unmarshal into the SDK's
// map[string]string fails and the message blocks the partition forever.
func TestKafkaPropertiesStringified(t *testing.T) {
	exec := loadKafkaMapping(t)
	out := queryKafkaMapping(t, exec, map[string]any{
		"event_name":           "api_call",
		"external_customer_id": "cust_123",
		"properties": map[string]any{
			"tokens": 42,
			"cost":   1.5,
			"ok":     true,
			"model":  "gpt-4o",
		},
	})

	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map", out["properties"])
	}
	for k, want := range map[string]string{
		"tokens": "42",
		"cost":   "1.5",
		"ok":     "true",
		"model":  "gpt-4o",
	} {
		got, isStr := props[k].(string)
		if !isStr {
			t.Errorf("properties[%s] = %v (%T), want a string", k, props[k], props[k])
			continue
		}
		if got != want {
			t.Errorf("properties[%s] = %q, want %q", k, got, want)
		}
	}

	// Top-level fields untouched.
	if got := out["event_name"]; got != "api_call" {
		t.Errorf("event_name = %v, want passthrough", got)
	}
	if got := out["external_customer_id"]; got != "cust_123" {
		t.Errorf("external_customer_id = %v, want passthrough", got)
	}
}

// TestKafkaPropertiesMissingOrScalar pins the guard: when properties is absent
// or not an object, the output is an empty object, never a poison value.
func TestKafkaPropertiesMissingOrScalar(t *testing.T) {
	exec := loadKafkaMapping(t)

	for name, in := range map[string]any{
		"absent": map[string]any{
			"event_name": "e", "external_customer_id": "c",
		},
		"scalar": map[string]any{
			"event_name": "e", "external_customer_id": "c",
			"properties": "not-an-object",
		},
	} {
		out := queryKafkaMapping(t, exec, in)
		props, ok := out["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s: properties is %T, want empty map", name, out["properties"])
			continue
		}
		if len(props) != 0 {
			t.Errorf("%s: properties = %v, want empty map", name, props)
		}
	}
}
