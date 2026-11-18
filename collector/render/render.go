// Package render assembles a collector's Bento pipeline: the window, the
// source provider's fetch stages, then the shared row → event mapping.
package render

import (
	"github.com/flexprice/bento-collector/collector/internal/blobl"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/spec"

	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const rfc3339 = `"2006-01-02T15:04:05Z07:00"`

// Pipeline is one window without an output; the engine attaches its own
// consumer so preview and run use exactly this pipeline.
func Pipeline(p source.Provider, in source.Input) (string, error) {
	st, err := p.Stages(in)
	if err != nil {
		return "", err
	}
	return build(in.Collector, st, windowFixed(in), nil, map[string]any{"drop": map[string]any{}}, "1", "")
}

// Standalone is a config that runs with plain `bento-flexprice -c`, gated by
// the collector's completeness check and sending to output. With a window it
// runs once for that day; without, it stays up and runs on trigger.schedule
// for the day before (or COLLECTOR_DAY=YYYY-MM-DD).
func Standalone(p source.Provider, in source.Input, output map[string]any, scheduled bool) (string, error) {
	st, err := p.Stages(in)
	if err != nil {
		return "", err
	}
	var gate []any
	if cp := in.Collector.Trigger.Completeness; cp != nil {
		g, err := p.Gate(in, *cp)
		if err != nil {
			return "", err
		}
		gate = append(g, map[string]any{"label": "completeness_gate", "mapping": fmt.Sprintf(
			`root = if @collector_complete != true { throw("Day " + @usage_date + " is not fully measured (completeness below %v); nothing sent. Re-run with COLLECTOR_DAY=" + @usage_date + " once the data is complete.") } else { this }`, cp.Min)})
	}
	if !scheduled {
		return build(in.Collector, st, windowFixed(in), gate, output, "1", "")
	}
	return build(in.Collector, st, windowScheduled(), gate, output, "0", "TZ=UTC "+in.Collector.Trigger.Schedule)
}

// The window mapping sets the metadata every later stage reads: window_start,
// window_end (RFC 3339), window_end_unix, usage_date (YYYY-MM-DD) and now_unix.
func windowFixed(in source.Input) string {
	return fmt.Sprintf("let start = %d\nlet now = %d\n", in.Window.Start.Unix(), in.Now.Unix()) + windowMeta
}

func windowScheduled() string {
	return `let day = env("COLLECTOR_DAY").or("")
let today = (now().ts_format("2006-01-02", "UTC") + "T00:00:00Z").ts_parse(` + rfc3339 + `).ts_unix()
let start = if $day != "" { ($day + "T00:00:00Z").ts_parse(` + rfc3339 + `).ts_unix() } else { $today - 86400 }
let now = now().ts_unix()
let _open = if $start + 86400 > $now { throw("COLLECTOR_DAY " + $day + " has not finished in UTC") }
` + windowMeta
}

const windowMeta = `meta window_start = $start.ts_format(` + rfc3339 + `, "UTC")
meta window_end = ($start + 86400).ts_format(` + rfc3339 + `, "UTC")
meta window_end_unix = $start + 86400
meta usage_date = $start.ts_format("2006-01-02", "UTC")
meta now_unix = $now
root = {}
`

func build(c spec.Collector, st source.Stages, window string, gate []any, output map[string]any, count, interval string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	mapping, err := eventMapping(c)
	if err != nil {
		return "", err
	}
	steps := append([]any{}, gate...)
	steps = append(steps, st.Fetch...)
	steps = append(steps,
		// Errors after this marker belong to one row; errors before it fail the whole query.
		map[string]any{"mutation": `meta collector_row = true`},
		map[string]any{"label": "normalize_row", "mapping": st.Normalize},
		map[string]any{"label": "flexprice_event", "mapping": mapping},
	)
	n := 1
	if count == "0" {
		n = 0
	}
	conf := map[string]any{
		"http":   map[string]any{"enabled": false},
		"logger": map[string]any{"level": "WARN", "format": "logfmt"},
		"input": map[string]any{"generate": map[string]any{
			"count":    n,
			"interval": interval,
			"mapping":  window,
		}},
		"pipeline": map[string]any{
			"threads":    1,
			"processors": []any{map[string]any{"try": steps}},
		},
		"output": output,
	}
	var buf bytes.Buffer
	if interval != "" {
		fmt.Fprintf(&buf, "# Rendered from collector %s v%d; runs at %q (UTC) for the previous day.\n# Backfill a day: COLLECTOR_DAY=YYYY-MM-DD, or render it with --day. Do not edit; change the spec instead.\n", c.ID, c.Version, c.Trigger.Schedule)
	} else {
		fmt.Fprintf(&buf, "# Rendered from collector %s v%d for one day. Do not edit; change the spec instead.\n", c.ID, c.Version)
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(conf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// eventMapping turns one normalized row ({labels, value, at}) into one
// Flexprice event. Every user value is embedded as a JSON string literal.
func eventMapping(c spec.Collector) (string, error) {
	m := c.Mapping
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w(`let labels = this.labels.or({})`)
	w(`let raw = this.value.string()`)
	if m.Customer.From == spec.CustomerLabel {
		skip, err := blobl.Literal(append([]string{}, m.Customer.Skip...))
		if err != nil {
			return "", err
		}
		w(`let key = $labels.get(%s).or("")`, blobl.Quote(m.Customer.Label))
		w(`let skip = %s.contains($key)`, skip)
	} else {
		w(`let skip = false`)
	}
	w(`let value = if $raw.re_match("^(NaN|[+-]?Inf)$") { if $skip { 0 } else { throw("Non-finite value " + $raw) } } else { $raw.number() }`)
	w(`let _neg = if !$skip && $value < 0 { throw("Negative value " + $raw) }`)
	w(`let _eval = if @evaluated_at != null && this.at != @evaluated_at { throw("Unexpected evaluation time " + this.at.string()) }`)
	for _, l := range m.RequiredLabels {
		w(`let _req = if !$skip && $labels.get(%s).or("") == "" { throw(%s) }`, blobl.Quote(l), blobl.Quote("Missing required label "+l))
	}
	if c.Source.Mode == spec.ModeUsageDate {
		w(`let _day = if $labels.usage_date.or("") != @usage_date { throw("usage_date " + $labels.usage_date.or("(missing)") + " is not " + @usage_date) }`)
	}
	switch m.Customer.From {
	case spec.CustomerStatic:
		w(`let customer = %s`, blobl.Quote(m.Customer.Value))
	case spec.CustomerLabel:
		w(`let _key = if $key == "" { throw(%s) }`, blobl.Quote("Missing customer label "+m.Customer.Label))
		if len(m.Customer.Lookup) > 0 {
			lookup, err := blobl.Literal(m.Customer.Lookup)
			if err != nil {
				return "", err
			}
			w(`let customer = if $skip { "" } else { %s.get($key).or(throw("No customer mapped for " + %s + "=" + $key)) }`, lookup, blobl.Quote(m.Customer.Label))
		} else {
			w(`let customer = $key`)
		}
	}
	drop := append([]string{"__name__"}, m.DropLabels...)
	w(`let kept = $labels.without(%s).map_each(p -> p.value.string())`, strings.Join(blobl.QuoteAll(drop), ", "))
	// Same inputs give the same event_id, so re-runs and backfills are idempotent.
	w(`let identity = [%s, %s, $customer, @query, 86400, @window_end_unix, $labels.key_values().sort_by(p -> p.key)]`,
		blobl.Quote(c.ID), blobl.Quote(m.EventName))

	props := map[string]string{"unit": m.Unit, "window_seconds": "86400"}
	for k, v := range m.Properties {
		props[k] = v
	}
	if m.Unit == "" {
		delete(props, "unit")
	}
	propsLit, err := blobl.Literal(props)
	if err != nil {
		return "", err
	}
	ts := "@window_start"
	if m.Timestamp == spec.TimestampWindowEnd {
		ts = "@window_end"
	}
	w(`meta collector_skip = if $skip { "true" }`)
	w(`root = if $skip { {"skipped": $key} } else { {`)
	w(`  "event_id": "col_" + $identity.string().hash("sha256").encode("hex").slice(0, 40),`)
	w(`  "event_name": %s,`, blobl.Quote(m.EventName))
	w(`  "external_customer_id": $customer,`)
	w(`  "timestamp": %s,`, ts)
	w(`  "source": %s,`, blobl.Quote(m.EventSource))
	w(`  "properties": $kept.merge(%s).merge({"window_start": @window_start, "window_end": @window_end, "value": $raw})`, propsLit)
	w(`} }`)
	return b.String(), nil
}
