// Package render assembles a collector's Bento pipeline: the window, the
// source provider's fetch stages, then the shared row → event mapping.
package render

import (
	"github.com/flexprice/bento-collector/collector/internal/blobl"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/spec"

	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const rfc3339 = `"2006-01-02T15:04:05Z07:00"`

// Pipeline is one window without an output; preview attaches its own consumer
// so it sees exactly the rows a published config sends.
func Pipeline(p source.Provider, in source.Input) (string, error) {
	st, err := p.Stages(in)
	if err != nil {
		return "", err
	}
	return build(in.Collector, st, windowFixed(in), nil, map[string]any{"drop": map[string]any{}}, 1, "", in.Window)
}

// Completeness is one window that only evaluates the completeness gate, so
// preview reports the same verdict a published config acts on.
func Completeness(p source.Provider, in source.Input, cp spec.Completeness) (string, error) {
	gate, err := p.Gate(in, cp)
	if err != nil {
		return "", err
	}
	return encode("", map[string]any{
		"input":    generate(1, "", windowFixed(in)),
		"pipeline": map[string]any{"threads": 1, "processors": gate},
		"output":   map[string]any{"drop": map[string]any{}},
	})
}

// Standalone is a Bento stream config gated by the collector's completeness
// check and sending to output. Published, it runs in `bento-flexprice streams`
// on the trigger's cron for the last closed window; otherwise it runs once for
// in.Window with plain `bento-flexprice -c` (a backfill).
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
			`root = if @collector_complete != true { throw("Window " + @window_start + " is not fully measured (completeness below %v); nothing sent. Backfill it with render --window " + @window_start + " once the data is complete.") } else { this }`, cp.Min)})
	}
	if !scheduled {
		return build(in.Collector, st, windowFixed(in), gate, output, 1, "", in.Window)
	}
	t := in.Collector.Trigger
	return build(in.Collector, st, windowScheduled(t.Size(), t.DelayDuration()), gate, output, 0, "TZ=UTC "+t.Cron(), in.Window)
}

// generate triggers the pipeline. Nacked messages (rejected rows, a failed
// gate) are not replayed: that would re-run the whole window in a loop.
func generate(count int, interval, mapping string) map[string]any {
	return map[string]any{"generate": map[string]any{
		"count":             count,
		"interval":          interval,
		"mapping":           mapping,
		"auto_replay_nacks": false,
	}}
}

// The window mapping sets the metadata every later stage reads: window_start
// and window_end (RFC 3339), window_start_unix, window_end_unix,
// window_seconds, window_length (PromQL duration), usage_date (YYYY-MM-DD of
// the start) and now_unix.
func windowFixed(in source.Input) string {
	return fmt.Sprintf("let size = %d\nlet start = %d\nlet now = %d\n", in.Window.Seconds(), in.Window.Start.Unix(), in.Now.Unix()) + windowMeta(in.Window.Length())
}

// windowScheduled picks the newest window that closed at least delay ago,
// like spec.LastClosed.
func windowScheduled(size, delay time.Duration) string {
	w := spec.WindowOf(time.Unix(0, 0), size)
	return fmt.Sprintf(`let size = %d
let now = now().ts_unix()
let start = (($now - %d) / $size).floor().int64() * $size - $size
`, w.Seconds(), int64(delay/time.Second)) + windowMeta(w.Length())
}

func windowMeta(length string) string {
	return `meta window_start = $start.ts_format(` + rfc3339 + `, "UTC")
meta window_end = ($start + $size).ts_format(` + rfc3339 + `, "UTC")
meta window_start_unix = $start
meta window_end_unix = $start + $size
meta window_seconds = $size
meta window_length = ` + blobl.Quote(length) + `
meta usage_date = $start.ts_format("2006-01-02", "UTC")
meta now_unix = $now
root = {}
`
}

func build(c spec.Collector, st source.Stages, window string, gate []any, output map[string]any, count int, interval string, w spec.Window) (string, error) {
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
	header := fmt.Sprintf("# Rendered from collector %s v%d for the %s window starting %s. Do not edit; change the spec instead.\n", c.ID, c.Version, w.Length(), w.Start.Format(time.RFC3339))
	if interval != "" {
		header = fmt.Sprintf("# Rendered from collector %s v%d; runs at %q (UTC) for the last %s window that closed %s earlier.\n# Do not edit; change the spec and publish it instead.\n", c.ID, c.Version, c.Trigger.Cron(), c.Trigger.Window, c.Trigger.Delay)
	}
	// Only stream fields (input, pipeline, output), so the same file runs in
	// streams mode and with -c.
	return encode(header, map[string]any{
		"input": generate(count, interval, window),
		"pipeline": map[string]any{
			"threads":    1,
			"processors": []any{map[string]any{"try": steps}},
		},
		"output": output,
	})
}

func encode(header string, conf map[string]any) (string, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
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
	w(`let value = if $raw.re_match("^(NaN|[+-]?Inf)$") { throw("Non-finite value " + $raw) } else { $raw.number() }`)
	w(`let _neg = if $value < 0 { throw("Negative value " + $raw) }`)
	w(`let _eval = if @evaluated_at != null && this.at != @evaluated_at { throw("Unexpected evaluation time " + this.at.string()) }`)
	for _, l := range m.RequiredLabels {
		w(`let _req = if $labels.get(%s).or("") == "" { throw(%s) }`, blobl.Quote(l), blobl.Quote("Missing required label "+l))
	}
	if c.Source.Mode == spec.ModeUsageDate {
		w(`let _day = if $labels.usage_date.or("") != @usage_date { throw("usage_date " + $labels.usage_date.or("(missing)") + " is not " + @usage_date) }`)
	}
	switch m.Customer.From {
	case spec.CustomerStatic:
		w(`let customer = %s`, blobl.Quote(m.Customer.Value))
	case spec.CustomerLabel:
		w(`let customer = $labels.get(%s).or("")`, blobl.Quote(m.Customer.Label))
		w(`let _customer = if $customer == "" { throw(%s) }`, blobl.Quote("Missing customer label "+m.Customer.Label))
	}
	// Properties from labels; a row without the label is rejected, since a
	// meter filtering on the property would not count it.
	keys := make([]string, 0, len(m.PropertyLabels))
	for k := range m.PropertyLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fromLabels := make([]string, len(keys))
	for i, k := range keys {
		label := blobl.Quote(m.PropertyLabels[k])
		w(`let _p%d = if $labels.get(%s).or("") == "" { throw(%s) }`, i, label, blobl.Quote("Missing label "+m.PropertyLabels[k]+" for property "+k))
		fromLabels[i] = fmt.Sprintf(`%s: $labels.get(%s).string()`, blobl.Quote(k), label)
	}
	// Same inputs give the same event_id, so re-runs and backfills are idempotent.
	w(`let identity = [%s, %s, $customer, @query, @window_seconds, @window_end_unix, $labels.key_values().sort_by(p -> p.key)]`,
		blobl.Quote(c.ID), blobl.Quote(m.EventName))

	propsLit, err := blobl.Literal(nonNil(m.Properties))
	if err != nil {
		return "", err
	}
	ts := "@window_start"
	if m.Timestamp == spec.TimestampWindowEnd {
		ts = "@window_end"
	}
	w(`root = {`)
	w(`  "event_id": "col_" + $identity.string().hash("sha256").encode("hex").slice(0, 40),`)
	w(`  "event_name": %s,`, blobl.Quote(m.EventName))
	w(`  "external_customer_id": $customer,`)
	w(`  "timestamp": %s,`, ts)
	w(`  "source": %s,`, blobl.Quote(m.EventSource))
	w(`  "properties": {%s}.merge(%s).merge({%s: $raw})`, strings.Join(fromLabels, ", "), propsLit, blobl.Quote(m.ValueProperty))
	w(`}`)
	return b.String(), nil
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
