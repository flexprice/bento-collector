package output

import (
	"os"
	"strings"
	"testing"

	"github.com/warpstreamlabs/bento/public/bloblang"
	"gopkg.in/yaml.v3"

	// Registers the full Bloblang method set (format_timestamp, etc.) that the
	// shipped mapping uses — same import main.go relies on.
	_ "github.com/warpstreamlabs/bento/public/components/all"
)

// sqsConfigPath is the shipped config whose transform we test. Parsing the
// mapping out of the config itself (rather than a copy) keeps the two in sync:
// if the shipped transform breaks, this test breaks.
const sqsConfigPath = "../internal/aws-sqs-to-flexprice.yaml"

// extractSQSMapping pulls the SQS-body -> Flexprice-event mapping (the pipeline
// processor whose bloblang stamps source = "aws-sqs-bento") out of the config.
func extractSQSMapping(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(sqsConfigPath)
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
		m, ok := p["mapping"].(string)
		if ok && strings.Contains(m, "aws-sqs-bento") {
			return m
		}
	}
	t.Fatal("could not find the SQS->Flexprice mapping processor in the config")
	return ""
}

func loadSQSMapping(t *testing.T) *bloblang.Executor {
	t.Helper()
	exec, err := bloblang.GlobalEnvironment().Parse(extractSQSMapping(t))
	if err != nil {
		t.Fatalf("parse mapping: %v", err)
	}
	return exec
}

func mustQuery(t *testing.T, exec *bloblang.Executor, in any) map[string]any {
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

// TestSQSMappingHappyPath pins the SQS-body -> Flexprice-event contract for a
// well-formed message: required fields carried over, properties stringified,
// source stamped.
func TestSQSMappingHappyPath(t *testing.T) {
	exec := loadSQSMapping(t)
	out := mustQuery(t, exec, map[string]any{
		"event_name":           "api_call",
		"external_customer_id": "cust_123",
		"timestamp":            "2024-01-02T03:04:05Z",
		"event_id":             "evt_1",
		"properties": map[string]any{
			"tokens": 42,       // int -> "42"
			"model":  "gpt-4o", // string stays
			"ok":     true,     // bool -> "true"
		},
	})

	if got := out["event_name"]; got != "api_call" {
		t.Errorf("event_name = %v, want api_call", got)
	}
	if got := out["external_customer_id"]; got != "cust_123" {
		t.Errorf("external_customer_id = %v, want cust_123", got)
	}
	if got := out["timestamp"]; got != "2024-01-02T03:04:05Z" {
		t.Errorf("timestamp = %v, want passthrough", got)
	}
	if got := out["source"]; got != "aws-sqs-bento" {
		t.Errorf("source = %v, want aws-sqs-bento", got)
	}

	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map", out["properties"])
	}
	for k, want := range map[string]string{"tokens": "42", "model": "gpt-4o", "ok": "true"} {
		if got := props[k]; got != want {
			t.Errorf("properties[%s] = %v (%T), want %q string", k, got, got, want)
		}
	}
}

// TestSQSMappingCustomerIDFallback pins the documented fallback: external_customer_id
// falls back to customer_id when absent.
func TestSQSMappingCustomerIDFallback(t *testing.T) {
	exec := loadSQSMapping(t)
	out := mustQuery(t, exec, map[string]any{
		"event_name":  "api_call",
		"customer_id": "cust_fallback",
	})
	if got := out["external_customer_id"]; got != "cust_fallback" {
		t.Errorf("external_customer_id = %v, want cust_fallback (from customer_id)", got)
	}
}

// TestSQSMappingDefaults pins the safe defaults when optional fields are missing:
// empty properties object, non-empty generated timestamp, empty optional strings.
func TestSQSMappingDefaults(t *testing.T) {
	exec := loadSQSMapping(t)
	out := mustQuery(t, exec, map[string]any{
		"event_name":           "api_call",
		"external_customer_id": "cust_123",
		// no properties, no timestamp, no event_id, no customer_id
	})

	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map", out["properties"])
	}
	if len(props) != 0 {
		t.Errorf("properties = %v, want empty map when body has none", props)
	}
	if ts, _ := out["timestamp"].(string); ts == "" {
		t.Error("timestamp is empty, want a generated fallback timestamp")
	}
	if got := out["event_id"]; got != "" {
		t.Errorf("event_id = %v, want empty string default", got)
	}
	if got := out["customer_id"]; got != "" {
		t.Errorf("customer_id = %v, want empty string default", got)
	}
}
