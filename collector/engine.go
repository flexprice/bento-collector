package collector

import (
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
	Store   Store
	Secrets Secrets
	HTTP    *http.Client
	Now     func() time.Time
}

func NewEngine(store Store, secrets Secrets) *Engine {
	return &Engine{Store: store, Secrets: secrets, HTTP: &http.Client{Timeout: 60 * time.Second}, Now: time.Now}
}

type Row struct {
	Series json.RawMessage `json:"series,omitempty"`
	Event  map[string]any  `json:"event,omitempty"`
	Error  string          `json:"error,omitempty"`
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
	Window       Window              `json:"window"`
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

// Resolve loads the connections and secrets a collector needs.
func (e *Engine) Resolve(c Collector, w Window, withSecrets bool) (RenderInput, error) {
	in := RenderInput{Collector: c, Window: w, QueryTime: e.Now()}
	var err error
	if in.Source, err = e.Store.GetConnection(c.Source.Connection); err != nil {
		return in, fmt.Errorf("source connection: %w", err)
	}
	if in.Destination, err = e.Store.GetConnection(c.Destination); err != nil {
		return in, fmt.Errorf("destination connection: %w", err)
	}
	if in.Destination.Type != ConnectionFlexprice {
		return in, fmt.Errorf("destination %q is not a flexprice connection", c.Destination)
	}
	if !withSecrets {
		return in, nil
	}
	if in.Source.SecretRef != "" {
		if in.Credentials.SourceSecret, err = e.Secrets.Get(in.Source.SecretRef); err != nil {
			return in, err
		}
	}
	in.Credentials.DestinationSecret, err = e.Secrets.Get(in.Destination.SecretRef)
	return in, err
}

func (e *Engine) prometheus(in RenderInput) PrometheusClient {
	return PrometheusClient{HTTP: e.HTTP, Conn: in.Source, Secret: in.Credentials.SourceSecret}
}

func (e *Engine) flexprice(in RenderInput) FlexpriceClient {
	return FlexpriceClient{HTTP: e.HTTP, Conn: in.Destination, APIKey: in.Credentials.DestinationSecret}
}

// Preview runs the collector for a window and validates the events without sending.
func (e *Engine) Preview(ctx context.Context, c Collector, w Window) (*Result, error) {
	return e.execute(ctx, c, w, true)
}

// Run executes the collector and sends valid events. Rows that fail mapping or
// reference unknown customers are reported and not sent; a failed completeness
// check, query or missing meter sends nothing.
func (e *Engine) Run(ctx context.Context, c Collector, w Window, dryRun bool) (*Result, error) {
	return e.execute(ctx, c, w, dryRun)
}

func (e *Engine) execute(ctx context.Context, c Collector, w Window, dryRun bool) (*Result, error) {
	c.Normalize()
	res := &Result{Collector: c.ID, Version: c.Version, Window: w, DryRun: dryRun, StartedAt: e.Now().UTC(), Rows: []Row{}, Checks: []Check{}}
	defer func() { res.FinishedAt = e.Now().UTC() }()
	in, err := e.Resolve(c, w, true)
	if err != nil {
		return nil, err
	}
	res.Query = in.query()
	res.EvaluatedAt = in.evalTime()

	if cp := c.Trigger.Completeness; cp != nil {
		res.Completeness = e.completeness(ctx, in, *cp)
		if !res.Completeness.OK {
			if res.Completeness.Error != "" {
				res.block("Completeness check failed: %s", res.Completeness.Error)
			} else {
				res.block("Day is not fully measured (completeness %v, need ≥ %v). Nothing will be sent; re-run once the data is complete.", res.Completeness.Values, cp.Min)
			}
		}
	}

	conf, err := RenderPipeline(in)
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

	fp := e.flexprice(in)
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

func (e *Engine) completeness(ctx context.Context, in RenderInput, cp Completeness) *CompletenessResult {
	out := &CompletenessResult{Query: cp.Query, Min: cp.Min, Values: []float64{}}
	samples, err := e.prometheus(in).Query(ctx, cp.Query, in.Window.End)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if len(samples) == 0 {
		out.Error = "the completeness query returned no data for this day"
		return out
	}
	out.OK = true
	for _, s := range samples {
		out.Values = append(out.Values, s.Value)
		if !(s.Value >= cp.Min) { // NaN never passes
			out.OK = false
		}
	}
	return out
}

func (e *Engine) checkCustomers(ctx context.Context, fp FlexpriceClient, res *Result) {
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
// message, keeping the mapping error on rows that failed.
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
			var series struct {
				Metric map[string]any `json:"metric"`
			}
			if json.Unmarshal(raw, &series) != nil || series.Metric == nil {
				mu.Lock()
				queryErrs = append(queryErrs, assignmentPrefix.ReplaceAllString(err.Error(), ""))
				mu.Unlock()
				return nil
			}
			row.Error = assignmentPrefix.ReplaceAllString(err.Error(), "")
			row.Series = append(json.RawMessage(nil), raw...)
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
