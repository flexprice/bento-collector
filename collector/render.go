package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Credentials are resolved secret values. Empty fields render as env-var
// placeholders so an exported config never contains a secret.
type Credentials struct {
	SourceSecret      string
	DestinationSecret string
}

// RenderInput is everything needed to produce one Bento config.
type RenderInput struct {
	Collector   Collector
	Source      Connection
	Destination Connection
	Window      Window
	// QueryTime is the evaluation time for usage_date mode.
	QueryTime   time.Time
	Credentials Credentials
}

func (in RenderInput) query() string {
	return strings.ReplaceAll(in.Collector.Source.Query, UsageDatePlaceholder, in.Window.Start.Format("2006-01-02"))
}

func (in RenderInput) evalTime() time.Time {
	if in.Collector.Source.Mode == ModeUsageDate {
		return in.QueryTime.UTC().Truncate(time.Second)
	}
	return in.Window.End
}

// RenderPipeline returns the input and processors only; the engine attaches
// its own consumer so preview and run use exactly the same pipeline.
func RenderPipeline(in RenderInput) (string, error) {
	return render(in, false)
}

// RenderConfig returns a complete Bento config including the flexprice output,
// for export, lint and running with plain `bento-flexprice -c`.
func RenderConfig(in RenderInput) (string, error) {
	return render(in, true)
}

func render(in RenderInput, withOutput bool) (string, error) {
	c := in.Collector
	if err := c.Validate(); err != nil {
		return "", err
	}
	if in.Source.Type != ConnectionPrometheus {
		return "", fmt.Errorf("source connection %q is not a prometheus connection", in.Source.ID)
	}
	body := url.Values{}
	body.Set("query", in.query())
	body.Set("time", in.evalTime().Format(time.RFC3339))

	httpProc := map[string]any{
		"url":     strings.TrimRight(in.Source.URL, "/") + "/api/v1/query",
		"verb":    "POST",
		"headers": sourceHeaders(in),
		"timeout": "30s",
		"retries": 3,
	}
	if in.Source.AuthType == "basic" {
		httpProc["basic_auth"] = map[string]any{
			"enabled":  true,
			"username": in.Source.Username,
			"password": secretOr(in.Credentials.SourceSecret, secretEnv(in.Source.SecretRef, "PROMETHEUS_PASSWORD")),
		}
	}

	mapping, err := eventMapping(in)
	if err != nil {
		return "", err
	}
	conf := map[string]any{
		"http":   map[string]any{"enabled": false},
		"logger": map[string]any{"level": "WARN", "format": "logfmt"},
		"input": map[string]any{
			"generate": map[string]any{
				"count":    1,
				"interval": "",
				"mapping":  "root = " + quote(body.Encode()),
			},
		},
		"pipeline": map[string]any{
			"threads": 1,
			"processors": []any{
				map[string]any{"try": []any{
					map[string]any{"label": "prometheus_query", "http": httpProc},
					map[string]any{"label": "prometheus_result", "mapping": responseMapping},
					map[string]any{"unarchive": map[string]any{"format": "json_array"}},
					map[string]any{"label": "flexprice_event", "mapping": mapping},
				}},
			},
		},
	}
	if !withOutput {
		// The engine fans out to this and its own consumer; drop keeps stdout clean.
		conf["output"] = map[string]any{"drop": map[string]any{}}
	}
	if withOutput {
		u, err := url.Parse(in.Destination.URL)
		if err != nil {
			return "", fmt.Errorf("destination url: %w", err)
		}
		conf["output"] = map[string]any{"switch": map[string]any{"cases": []any{
			map[string]any{"check": "errored()", "output": map[string]any{"reject": "collector " + c.ID + " rejected a row: ${! error() }"}},
			map[string]any{"check": `@collector_skip == "true"`, "output": map[string]any{"drop": map[string]any{}}},
			map[string]any{"output": map[string]any{"flexprice": map[string]any{
				"api_host": u.Host,
				"scheme":   u.Scheme,
				"api_key":  secretOr(in.Credentials.DestinationSecret, secretEnv(in.Destination.SecretRef, "FLEXPRICE_API_KEY")),
				"batching": map[string]any{"count": 1000, "period": "1s"},
			}}},
		}}}
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Rendered from collector %s v%d for %s. Do not edit; change the spec instead.\n",
		c.ID, c.Version, in.Window.Start.Format("2006-01-02"))
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(conf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func sourceHeaders(in RenderInput) map[string]any {
	h := map[string]any{"Content-Type": "application/x-www-form-urlencoded"}
	for k, v := range in.Source.Headers {
		h[k] = v
	}
	if in.Source.AuthType == "bearer" {
		h["Authorization"] = "Bearer " + secretOr(in.Credentials.SourceSecret, secretEnv(in.Source.SecretRef, "PROMETHEUS_TOKEN"))
	}
	return h
}

func secretOr(v, env string) string {
	if v != "" {
		return v
	}
	return "${" + env + "}"
}

// secretEnv is the variable an exported config reads a secret from: the
// connection's own secret_env when it has one.
func secretEnv(ref, fallback string) string {
	if name, ok := strings.CutPrefix(ref, envRefPrefix); ok {
		return name
	}
	return fallback
}

const responseMapping = `root = if this.status != "success" {
  throw("Prometheus query failed: " + this.error.or("unknown error"))
} else if this.data.resultType != "vector" {
  throw("Expected an instant vector, got " + this.data.resultType.string())
} else if this.warnings.or([]).length() > 0 {
  throw("Prometheus returned warnings, usage may be partial: " + this.warnings.join("; "))
} else { this.data.result }
`

// eventMapping builds the Bloblang that turns one Prometheus series into one
// Flexprice event. Every user value is embedded as a JSON string literal.
func eventMapping(in RenderInput) (string, error) {
	c := in.Collector
	m := c.Mapping
	start, end := in.Window.Start, in.Window.End
	ts := start
	if m.Timestamp == TimestampWindowEnd {
		ts = end
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w(`let labels = this.metric.or({})`)
	w(`let raw = this.value.index(1).string()`)
	if m.Customer.From == CustomerLabel {
		skip, err := jsonLiteral(append([]string{}, m.Customer.Skip...))
		if err != nil {
			return "", err
		}
		w(`let key = $labels.get(%s).or("")`, quote(m.Customer.Label))
		w(`let skip = %s.contains($key)`, skip)
	} else {
		w(`let skip = false`)
	}
	w(`let value = if $raw.re_match("^(NaN|[+-]?Inf)$") { if $skip { 0 } else { throw("Non-finite value " + $raw) } } else { $raw.number() }`)
	w(`let _neg = if !$skip && $value < 0 { throw("Negative value " + $raw) }`)
	w(`let _eval = if this.value.index(0).number() != %d { throw("Unexpected evaluation time " + this.value.index(0).string()) }`, in.evalTime().Unix())
	for _, l := range m.RequiredLabels {
		w(`let _req = if !$skip && $labels.get(%s).or("") == "" { throw(%s) }`, quote(l), quote("Missing required label "+l))
	}
	if c.Source.Mode == ModeUsageDate {
		day := start.Format("2006-01-02")
		w(`let _day = if $labels.usage_date.or("") != %s { throw("usage_date " + $labels.usage_date.or("(missing)") + " is not " + %s) }`, quote(day), quote(day))
	}
	switch m.Customer.From {
	case CustomerStatic:
		w(`let customer = %s`, quote(m.Customer.Value))
	case CustomerLabel:
		w(`let _key = if $key == "" { throw(%s) }`, quote("Missing customer label "+m.Customer.Label))
		if len(m.Customer.Lookup) > 0 {
			lookup, err := jsonLiteral(m.Customer.Lookup)
			if err != nil {
				return "", err
			}
			w(`let customer = if $skip { "" } else { %s.get($key).or(throw("No customer mapped for " + %s + "=" + $key)) }`, lookup, quote(m.Customer.Label))
		} else {
			w(`let customer = $key`)
		}
	}
	drop := append([]string{"__name__"}, m.DropLabels...)
	w(`let kept = $labels.without(%s).map_each(p -> p.value.string())`, strings.Join(quoteAll(drop), ", "))
	w(`let identity = [%s, %s, $customer, %s, 86400, %d, $labels.key_values().sort_by(p -> p.key)]`,
		quote(c.ID), quote(m.EventName), quote(in.query()), end.Unix())

	props := map[string]string{
		"unit":           m.Unit,
		"window_seconds": "86400",
		"window_start":   start.Format(time.RFC3339),
		"window_end":     end.Format(time.RFC3339),
	}
	for k, v := range m.Properties {
		props[k] = v
	}
	if m.Unit == "" {
		delete(props, "unit")
	}
	propsLit, err := jsonLiteral(props)
	if err != nil {
		return "", err
	}
	w(`meta collector_skip = if $skip { "true" }`)
	w(`root = if $skip { {"skipped": $key} } else { {`)
	w(`  "event_id": "col_" + $identity.string().hash("sha256").encode("hex").slice(0, 40),`)
	w(`  "event_name": %s,`, quote(m.EventName))
	w(`  "external_customer_id": $customer,`)
	w(`  "timestamp": %s,`, quote(ts.Format(time.RFC3339)))
	w(`  "source": %s,`, quote(m.EventSource))
	w(`  "properties": $kept.merge(%s).merge({"value": $raw})`, propsLit)
	w(`} }`)
	return b.String(), nil
}

func quote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(buf.String())
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quote(s)
	}
	return out
}

func jsonLiteral(v any) (string, error) {
	if m, ok := v.(map[string]string); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = quote(k) + ": " + quote(m[k])
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
