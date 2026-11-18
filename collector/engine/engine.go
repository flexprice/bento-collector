// Package engine previews and runs collectors for one usage window: completeness
// gate, the rendered Bento pipeline, Flexprice validation, then bulk send.
package engine

import (
	"github.com/flexprice/bento-collector/collector/flexprice"
	"github.com/flexprice/bento-collector/collector/render"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/spec"
	"github.com/flexprice/bento-collector/collector/store"

	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/warpstreamlabs/bento/public/service"

	// Components every rendered pipeline uses: generate, mapping, try,
	// unarchive (pure) and the http processor (io).
	_ "github.com/warpstreamlabs/bento/public/components/io"
	_ "github.com/warpstreamlabs/bento/public/components/pure"
)

type Engine struct {
	Store       store.Store
	Connections *store.Connections
	Sources     *source.Registry
	HTTP        *http.Client
	Now         func() time.Time
}

// New checks every connection against its provider before anything runs.
func New(st store.Store, conns *store.Connections, sources *source.Registry) (*Engine, error) {
	for _, c := range conns.List() {
		var err error
		if c.Type == flexprice.Type {
			err = flexprice.ValidateConnection(c)
		} else if p, perr := sources.Get(c.Type); perr != nil {
			err = perr
		} else {
			err = p.ValidateConnection(c)
		}
		if err != nil {
			return nil, fmt.Errorf("connection %q: %w", c.ID, err)
		}
	}
	return &Engine{Store: st, Connections: conns, Sources: sources, HTTP: &http.Client{Timeout: 60 * time.Second}, Now: time.Now}, nil
}

type Row struct {
	// Row is the source row a rejected event came from.
	Row   json.RawMessage `json:"row,omitempty"`
	Event map[string]any  `json:"event,omitempty"`
	Error string          `json:"error,omitempty"`
}

type Check struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type CompletenessResult struct {
	Query  string    `json:"query"`
	Min    float64   `json:"min"`
	Values []float64 `json:"values"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
}

// Result is the outcome of a preview or a run for one window.
type Result struct {
	Collector    string              `json:"collector"`
	Version      int                 `json:"version"`
	Window       spec.Window         `json:"window"`
	Query        string              `json:"query"`
	EvaluatedAt  time.Time           `json:"evaluated_at"`
	Completeness *CompletenessResult `json:"completeness,omitempty"`
	Rows         []Row               `json:"rows"`
	Events       int                 `json:"events"`
	Rejected     int                 `json:"rejected"`
	Skipped      int                 `json:"skipped"`
	Sent         int                 `json:"sent"`
	Checks       []Check             `json:"checks"`
	DryRun       bool                `json:"dry_run"`
	OK           bool                `json:"ok"`
	StartedAt    time.Time           `json:"started_at"`
	FinishedAt   time.Time           `json:"finished_at"`
	noSend       bool
}

func (r *Result) check(level, format string, args ...any) {
	r.Checks = append(r.Checks, Check{Level: level, Message: fmt.Sprintf(format, args...)})
}

// block records an error that stops the whole run from sending.
func (r *Result) block(format string, args ...any) {
	r.noSend = true
	r.check("error", format, args...)
}

func (r *Result) blocked() bool {
	for _, c := range r.Checks {
		if c.Level == "error" {
			return true
		}
	}
	return false
}

// plan is a collector bound to its provider, connections and secrets.
type plan struct {
	provider source.Provider
	in       source.Input
	dest     spec.Connection
	destKey  string
}

// resolve binds a collector to its connections. live resolves real secrets;
// otherwise they are ${ENV} placeholders for an exported config.
func (e *Engine) resolve(c spec.Collector, w spec.Window, live bool) (plan, error) {
	var pl plan
	if err := c.Validate(); err != nil {
		return pl, err
	}
	p, err := e.Sources.Get(c.Source.Type)
	if err != nil {
		return pl, err
	}
	src, err := e.Connections.Get(c.Source.Connection)
	if err != nil {
		return pl, fmt.Errorf("source connection: %w", err)
	}
	if src.Type != c.Source.Type {
		return pl, fmt.Errorf("source connection %q is %s, not %s", src.ID, src.Type, c.Source.Type)
	}
	if err := p.ValidateSource(c.Source); err != nil {
		return pl, err
	}
	dest, err := e.Connections.Get(c.Destination)
	if err != nil {
		return pl, fmt.Errorf("destination connection: %w", err)
	}
	if err := flexprice.ValidateConnection(dest); err != nil {
		return pl, fmt.Errorf("destination: %w", err)
	}
	pl = plan{provider: p, dest: dest, in: source.Input{Collector: c, Conn: src, Window: w, Now: e.Now()}}
	if !live {
		pl.in.Secret, pl.destKey = placeholder(src), placeholder(dest)
		return pl, nil
	}
	if pl.in.Secret, err = e.Connections.Secret(src); err != nil {
		return pl, err
	}
	pl.destKey, err = e.Connections.Secret(dest)
	return pl, err
}

func placeholder(c spec.Connection) string {
	if c.SecretEnv == "" {
		return ""
	}
	return "${" + c.SecretEnv + "}"
}

// Validate checks a collector against its provider and connections.
func (e *Engine) Validate(c spec.Collector) error {
	_, err := e.resolve(c, spec.DayWindow(e.Now()), false)
	return err
}

// Render returns a config for plain `bento-flexprice -c`: scheduled on
// trigger.schedule when day is nil, otherwise one run for that day.
func (e *Engine) Render(c spec.Collector, day *spec.Window) (string, error) {
	w := spec.DayWindow(e.Now().Add(-24 * time.Hour))
	if day != nil {
		w = *day
	}
	pl, err := e.resolve(c, w, false)
	if err != nil {
		return "", err
	}
	out, err := flexprice.Output(pl.dest, pl.destKey, c.ID)
	if err != nil {
		return "", err
	}
	return render.Standalone(pl.provider, pl.in, out, day == nil)
}

// TestConnection checks reachability and credentials of one connection.
func (e *Engine) TestConnection(ctx context.Context, id string) (string, error) {
	c, err := e.Connections.Get(id)
	if err != nil {
		return "", err
	}
	secret, err := e.Connections.Secret(c)
	if err != nil {
		return "", err
	}
	if c.Type == flexprice.Type {
		if err := e.flexprice(c, secret).Test(ctx); err != nil {
			return "", err
		}
		return "Connected to Flexprice; the API key works", nil
	}
	p, err := e.Sources.Get(c.Type)
	if err != nil {
		return "", err
	}
	return p.Test(ctx, source.Conn{Connection: c, Secret: secret, HTTP: e.HTTP})
}

func (e *Engine) flexprice(c spec.Connection, key string) flexprice.Client {
	return flexprice.Client{HTTP: e.HTTP, Conn: c, APIKey: key}
}

// Preview runs the collector for a window and validates the events without sending.
func (e *Engine) Preview(ctx context.Context, c spec.Collector, w spec.Window) (*Result, error) {
	return e.execute(ctx, c, w, true)
}

// Run executes the collector and sends valid events. Rows that fail mapping or
// reference unknown customers are reported and not sent; a failed completeness
// check, query or missing meter sends nothing.
func (e *Engine) Run(ctx context.Context, c spec.Collector, w spec.Window, dryRun bool) (*Result, error) {
	return e.execute(ctx, c, w, dryRun)
}

func (e *Engine) execute(ctx context.Context, c spec.Collector, w spec.Window, dryRun bool) (*Result, error) {
	c.Normalize()
	res := &Result{Collector: c.ID, Version: c.Version, Window: w, DryRun: dryRun, StartedAt: e.Now().UTC(), Rows: []Row{}, Checks: []Check{}}
	defer func() { res.FinishedAt = e.Now().UTC() }()
	pl, err := e.resolve(c, w, true)
	if err != nil {
		return nil, err
	}
	st, err := pl.provider.Stages(pl.in)
	if err != nil {
		return nil, err
	}
	res.Query = st.Query
	res.EvaluatedAt = st.EvaluatedAt

	if cp := c.Trigger.Completeness; cp != nil {
		res.Completeness = e.completeness(ctx, pl, *cp)
		if !res.Completeness.OK {
			if res.Completeness.Error != "" {
				res.block("Completeness check failed: %s", res.Completeness.Error)
			} else {
				res.block("Day is not fully measured (completeness %v, need ≥ %v). Nothing will be sent; re-run once the data is complete.", res.Completeness.Values, cp.Min)
			}
		}
	}

	conf, err := render.Pipeline(pl.provider, pl.in)
	if err != nil {
		return nil, err
	}
	rows, skipped, queryErrs, err := runPipeline(ctx, conf)
	if err != nil {
		return nil, err
	}
	res.Rows = rows
	res.Skipped = skipped
	if skipped > 0 {
		res.check("ok", "%d row(s) skipped: label values listed as not billed", skipped)
	}
	for _, qe := range queryErrs {
		res.block("Query failed: %s", qe)
	}

	fp := e.flexprice(pl.dest, pl.destKey)
	meters, err := fp.MetersFor(ctx, c.Mapping.EventName)
	switch {
	case err != nil:
		res.block("Could not check meters in Flexprice: %v", err)
	case len(meters) == 0:
		res.block("No Flexprice meter uses event_name %q. Events would be stored but never billed.", c.Mapping.EventName)
	default:
		res.check("ok", "Meter found for %q (%d)", c.Mapping.EventName, len(meters))
	}
	e.checkCustomers(ctx, fp, res)

	for _, r := range res.Rows {
		if r.Error != "" {
			res.Rejected++
		} else {
			res.Events++
		}
	}
	if res.Events+res.Rejected+res.Skipped == 0 {
		res.check("warning", "The query returned no rows: zero usage, a wrong tenant or data older than retention.")
	}
	if res.Rejected > 0 {
		res.check("error", "%d row(s) rejected and not sent; fix the mapping or data and re-run this day.", res.Rejected)
	}

	if !dryRun && !res.noSend {
		events := make([]map[string]any, 0, res.Events)
		for _, r := range res.Rows {
			if r.Error == "" {
				events = append(events, r.Event)
			}
		}
		sent, err := fp.SendBulk(ctx, events)
		res.Sent = sent
		if err != nil {
			res.block("Sending to Flexprice failed after %d of %d events (re-running is safe): %v", sent, len(events), err)
		}
	}
	res.OK = !res.blocked()
	return res, nil
}

func (e *Engine) completeness(ctx context.Context, pl plan, cp spec.Completeness) *CompletenessResult {
	out := &CompletenessResult{Query: cp.Query, Min: cp.Min, Values: []float64{}}
	values, err := pl.provider.Values(ctx, source.Conn{Connection: pl.in.Conn, Secret: pl.in.Secret, HTTP: e.HTTP}, cp.Query, pl.in.Window.End)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if len(values) == 0 {
		out.Error = "the completeness query returned no data for this day"
		return out
	}
	out.OK = true
	for _, v := range values {
		out.Values = append(out.Values, v)
		if !(v >= cp.Min) { // NaN never passes
			out.OK = false
		}
	}
	return out
}

func (e *Engine) checkCustomers(ctx context.Context, fp flexprice.Client, res *Result) {
	known := map[string]bool{}
	var missing []string
	for i, r := range res.Rows {
		if r.Error != "" {
			continue
		}
		id, _ := r.Event["external_customer_id"].(string)
		ok, seen := known[id]
		if !seen {
			exists, err := fp.CustomerExists(ctx, id)
			if err != nil {
				res.block("Could not check customer %q in Flexprice: %v", id, err)
				return
			}
			known[id], ok = exists, exists
			if !exists {
				missing = append(missing, id)
			}
		}
		if !ok {
			res.Rows[i].Error = fmt.Sprintf("Customer %q does not exist in Flexprice", id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		res.check("warning", "Unknown customers: %v. Create them in Flexprice or fix the customer mapping.", missing)
	} else if len(known) > 0 {
		res.check("ok", "All %d customer(s) exist in Flexprice", len(known))
	}
}

var assignmentPrefix = regexp.MustCompile(`^failed assignment \(line \d+\): `)

// runPipeline executes a rendered pipeline in-process and collects every
// message, keeping the mapping error on rows that failed. Errors on messages
// without the collector_row marker failed before rows existed (the query).
func runPipeline(ctx context.Context, conf string) ([]Row, int, []string, error) {
	sb := service.NewStreamBuilder()
	if err := sb.SetYAML(conf); err != nil {
		return nil, 0, nil, fmt.Errorf("rendered config is invalid: %w", err)
	}
	var mu sync.Mutex
	rows := []Row{}
	skipped := 0
	var queryErrs []string
	err := sb.AddConsumerFunc(func(_ context.Context, msg *service.Message) error {
		raw, _ := msg.AsBytes()
		if v, _ := msg.MetaGet("collector_skip"); v == "true" && msg.GetError() == nil {
			mu.Lock()
			skipped++
			mu.Unlock()
			return nil
		}
		row := Row{}
		if err := msg.GetError(); err != nil {
			if isRow, _ := msg.MetaGetMut("collector_row"); isRow != true {
				mu.Lock()
				queryErrs = append(queryErrs, assignmentPrefix.ReplaceAllString(err.Error(), ""))
				mu.Unlock()
				return nil
			}
			row.Error = assignmentPrefix.ReplaceAllString(err.Error(), "")
			row.Row = append(json.RawMessage(nil), raw...)
		} else {
			if err := json.Unmarshal(raw, &row.Event); err != nil {
				row.Error = "mapping produced invalid JSON: " + err.Error()
			}
		}
		mu.Lock()
		rows = append(rows, row)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, 0, nil, err
	}
	stream, err := sb.Build()
	if err != nil {
		return nil, 0, nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := stream.Run(runCtx); err != nil && err != context.Canceled {
		return nil, 0, nil, err
	}
	return rows, skipped, queryErrs, nil
}
