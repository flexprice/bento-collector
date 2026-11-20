// Package engine previews collectors for one usage window (completeness gate,
// the rendered Bento pipeline, Flexprice checks), samples source queries and
// lists Flexprice meters and customers for the UI, and renders the stream
// config that Bento runs once a version is published. It never sends events.
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
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
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

// Result is the outcome of a preview for one window.
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
	Checks       []Check             `json:"checks"`
	OK           bool                `json:"ok"`
	StartedAt    time.Time           `json:"started_at"`
	FinishedAt   time.Time           `json:"finished_at"`
}

type checks []Check

func (cs *checks) add(level, format string, args ...any) {
	*cs = append(*cs, Check{Level: level, Message: fmt.Sprintf(format, args...)})
}

// Blocked reports whether any check is an error.
func Blocked(cs []Check) bool {
	for _, c := range cs {
		if c.Level == "error" {
			return true
		}
	}
	return false
}

func (r *Result) check(level, format string, args ...any) {
	(*checks)(&r.Checks).add(level, format, args...)
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

func (e *Engine) lastClosed(c spec.Collector) spec.Window {
	return spec.LastClosed(c.Trigger.Size(), c.Trigger.DelayDuration(), e.Now())
}

// Validate checks a collector against its provider and connections.
func (e *Engine) Validate(c spec.Collector) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := e.resolve(c, e.lastClosed(c), false)
	return err
}

// Render returns the stream config: scheduled on the trigger's cron when w is
// nil (what Publish writes), otherwise one run for that window (a backfill).
func (e *Engine) Render(c spec.Collector, w *spec.Window) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	win := e.lastClosed(c)
	if w != nil {
		win = *w
	}
	pl, err := e.resolve(c, win, false)
	if err != nil {
		return "", err
	}
	out, err := flexprice.Output(pl.dest, pl.destKey, c.ID)
	if err != nil {
		return "", err
	}
	return render.Standalone(pl.provider, pl.in, out, w == nil)
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

// Preview runs the collector for a window and validates the events without
// sending: the completeness gate, the rendered pipeline, meter and customers.
func (e *Engine) Preview(ctx context.Context, c spec.Collector, w spec.Window) (*Result, error) {
	c.Normalize()
	res := &Result{Collector: c.ID, Version: c.Version, Window: w, StartedAt: e.Now().UTC(), Rows: []Row{}, Checks: []Check{}}
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
		if res.Completeness, err = e.completeness(ctx, pl, *cp); err != nil {
			return nil, err
		}
		if !res.Completeness.OK {
			if res.Completeness.Error != "" {
				res.check("error", "Completeness check failed: %s", res.Completeness.Error)
			} else {
				res.check("error", "The window is not fully measured (completeness %v, need ≥ %v). A published config sends nothing for this window.", res.Completeness.Values, cp.Min)
			}
		}
	}

	conf, err := render.Pipeline(pl.provider, pl.in)
	if err != nil {
		return nil, err
	}
	rows, queryErrs, err := runPipeline(ctx, conf)
	if err != nil {
		return nil, err
	}
	res.Rows = rows
	for _, qe := range queryErrs {
		res.check("error", "Query failed: %s", qe)
	}

	fp := e.flexprice(pl.dest, pl.destKey)
	if m := (*checks)(&res.Checks).meter(ctx, fp, c.Mapping); m != nil {
		checkFilters(res, *m)
	}
	e.checkCustomers(ctx, fp, res)

	for _, r := range res.Rows {
		if r.Error != "" {
			res.Rejected++
		} else {
			res.Events++
		}
	}
	if res.Events+res.Rejected == 0 {
		res.check("warning", "The query returned no rows: zero usage, a wrong tenant or data older than retention.")
	}
	if res.Rejected > 0 {
		res.check("error", "%d row(s) would be rejected and not sent; fix the mapping or data.", res.Rejected)
	}
	res.OK = !Blocked(res.Checks)
	return res, nil
}

// Publish renders the scheduled stream config for a saved version after
// checking Flexprice: the meter must exist and read the value property. An
// error check means the config must not be published; customers that do not
// exist yet are only a warning, since Flexprice stores their events.
func (e *Engine) Publish(ctx context.Context, c spec.Collector) (string, []Check, error) {
	c.Normalize()
	y, err := e.Render(c, nil)
	if err != nil {
		return "", nil, err
	}
	pl, err := e.resolve(c, e.lastClosed(c), true)
	if err != nil {
		return "", nil, err
	}
	fp := e.flexprice(pl.dest, pl.destKey)
	cs := checks{}
	cs.meter(ctx, fp, c.Mapping)
	if c.Mapping.Customer.From == spec.CustomerLabel {
		cs.add("ok", "Customers come from label %s, so new values are picked up without republishing.", c.Mapping.Customer.Label)
		return y, cs, nil
	}
	ids := []string{c.Mapping.Customer.Value}
	found, err := fp.ExistingCustomers(ctx, ids)
	if err != nil {
		cs.add("warning", "Could not check customers in Flexprice: %v", err)
		return y, cs, nil
	}
	var missing []string
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		cs.add("warning", "No Flexprice customer for %v yet. Their events are still sent and stored, but bill nobody until the customer exists.", missing)
	} else {
		cs.add("ok", "All %d customer(s) exist in Flexprice", len(ids))
	}
	return y, cs, nil
}

// meter checks that the collector's meter exists and reads the property the
// value is sent as, and returns it.
func (cs *checks) meter(ctx context.Context, fp flexprice.Client, m spec.Mapping) *flexprice.Meter {
	meters, err := fp.MetersFor(ctx, m.EventName)
	if err != nil {
		cs.add("error", "Could not check meters in Flexprice: %v", err)
		return nil
	}
	if len(meters) == 0 {
		cs.add("error", "No published Flexprice meter uses event_name %q. Events would be stored but never billed.", m.EventName)
		return nil
	}
	var target *flexprice.Meter
	for i := range meters {
		if (m.MeterID != "" && meters[i].ID == m.MeterID) || (m.MeterID == "" && target == nil) {
			target = &meters[i]
		}
	}
	if target == nil {
		cs.add("error", "Meter %s no longer uses event_name %q. Pick the meter again.", m.MeterID, m.EventName)
		return nil
	}
	if vp := target.ValueProperty(); vp != "" && vp != m.ValueProperty {
		cs.add("error", "Meter %q aggregates properties.%s, but the value is sent as properties.%s, so it would count nothing.", target.Name, vp, m.ValueProperty)
		return target
	}
	cs.add("ok", "Meter %q found (%s of %s)", target.Name, target.Aggregation.Type, orDash(target.ValueProperty()))
	return target
}

// checkFilters warns about events the meter's filters would not count: no
// filter property at all, or a value the filter does not allow.
func checkFilters(res *Result, m flexprice.Meter) {
	for _, f := range m.Filters {
		allowed := map[string]bool{}
		for _, v := range f.Values {
			allowed[v] = true
		}
		events, missing := 0, 0
		var rejected []string
		seen := map[string]bool{}
		for _, r := range res.Rows {
			props, ok := r.Event["properties"].(map[string]any)
			if !ok {
				continue
			}
			events++
			v, has := props[f.Key].(string)
			switch {
			case !has || v == "":
				missing++
			case len(allowed) > 0 && !allowed[v] && !seen[v]:
				seen[v] = true
				rejected = append(rejected, v)
			}
		}
		switch {
		case events == 0:
		case missing == events:
			res.check("warning", "Meter %q filters on %s, but no event carries it, so the meter counts none of them. Add an event property named %s.", m.Name, f.Key, f.Key)
		case len(rejected) > 0:
			sort.Strings(rejected)
			res.check("warning", "Meter %q only counts %s in %v; events with %v are sent but not counted.", m.Name, f.Key, f.Values, rejected)
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// completeness runs the rendered gate on its own, so preview shows the verdict
// a published config acts on.
func (e *Engine) completeness(ctx context.Context, pl plan, cp spec.Completeness) (*CompletenessResult, error) {
	out := &CompletenessResult{Query: cp.Query, Min: cp.Min, Values: []float64{}}
	conf, err := render.Completeness(pl.provider, pl.in, cp)
	if err != nil {
		return nil, err
	}
	sb := service.NewStreamBuilder()
	if err := sb.SetYAML(conf); err != nil {
		return nil, fmt.Errorf("rendered completeness config is invalid: %w", err)
	}
	if err := sb.AddConsumerFunc(func(_ context.Context, msg *service.Message) error {
		if err := msg.GetError(); err != nil {
			out.Error = assignmentPrefix.ReplaceAllString(err.Error(), "")
			return nil
		}
		ok, _ := msg.MetaGetMut("collector_complete")
		out.OK = ok == true
		if v, _ := msg.MetaGetMut("collector_completeness_error"); v != nil {
			out.Error = fmt.Sprint(v)
		}
		vals, _ := msg.MetaGetMut("collector_completeness")
		list, _ := vals.([]any)
		for _, v := range list {
			f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
			if err != nil {
				f = math.NaN()
			}
			out.Values = append(out.Values, f)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	stream, err := sb.Build()
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := stream.Run(runCtx); err != nil && err != context.Canceled {
		return nil, err
	}
	if out.Error != "" {
		out.OK = false
	}
	return out, nil
}

// checkCustomers warns about events for customers Flexprice does not know.
// They are still sent: Flexprice stores them, and bills them once the
// customer exists.
func (e *Engine) checkCustomers(ctx context.Context, fp flexprice.Client, res *Result) {
	seen := map[string]bool{}
	var ids []string
	for _, r := range res.Rows {
		id, _ := r.Event["external_customer_id"].(string)
		if r.Error == "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	found, err := fp.ExistingCustomers(ctx, ids)
	if err != nil {
		res.check("warning", "Could not check customers in Flexprice: %v", err)
		return
	}
	var missing []string
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		res.check("warning", "No Flexprice customer for %v. Their events are still sent and stored, but bill nobody until the customer exists.", missing)
	} else {
		res.check("ok", "All %d customer(s) exist in Flexprice", len(ids))
	}
}

// SampleResult is a source query's rows for one window, before any mapping.
type SampleResult struct {
	Window      spec.Window     `json:"window"`
	Query       string          `json:"query"`
	EvaluatedAt time.Time       `json:"evaluated_at"`
	Rows        []source.Sample `json:"rows"`
	Truncated   bool            `json:"truncated"`
}

const maxSampleRows = 5000

// Sample runs a source query for one window, so the UI can offer the labels
// and values it returns. It needs only the source and the window.
func (e *Engine) Sample(ctx context.Context, src spec.Source, t spec.Trigger, w spec.Window) (*SampleResult, error) {
	c := spec.Collector{Source: src, Trigger: t}
	c.Normalize()
	p, conn, err := e.source(c.Source.Connection, c.Source.Type)
	if err != nil {
		return nil, err
	}
	in := source.Input{Collector: c, Conn: conn.Connection, Window: w, Now: e.Now()}
	rows, err := p.Sample(ctx, conn, in)
	if err != nil {
		return nil, err
	}
	st, err := p.Stages(in)
	if err != nil {
		return nil, err
	}
	res := &SampleResult{Window: w, Query: st.Query, EvaluatedAt: st.EvaluatedAt, Rows: rows}
	if len(rows) > maxSampleRows {
		res.Rows, res.Truncated = rows[:maxSampleRows], true
	}
	return res, nil
}

func (e *Engine) source(connID, typ string) (source.Provider, source.Conn, error) {
	c, err := e.Connections.Get(connID)
	if err != nil {
		return nil, source.Conn{}, err
	}
	if typ != "" && c.Type != typ {
		return nil, source.Conn{}, fmt.Errorf("connection %q is %s, not %s", c.ID, c.Type, typ)
	}
	p, err := e.Sources.Get(c.Type)
	if err != nil {
		return nil, source.Conn{}, err
	}
	secret, err := e.Connections.Secret(c)
	if err != nil {
		return nil, source.Conn{}, err
	}
	return p, source.Conn{Connection: c, Secret: secret, HTTP: e.HTTP}, nil
}

// Destination is a Flexprice API client for a destination connection.
func (e *Engine) Destination(id string) (flexprice.Client, error) {
	c, err := e.Connections.Get(id)
	if err != nil {
		return flexprice.Client{}, err
	}
	if err := flexprice.ValidateConnection(c); err != nil {
		return flexprice.Client{}, err
	}
	key, err := e.Connections.Secret(c)
	if err != nil {
		return flexprice.Client{}, err
	}
	return e.flexprice(c, key), nil
}

var assignmentPrefix = regexp.MustCompile(`^failed assignment \(line \d+\): `)

// runPipeline executes a rendered pipeline in-process and collects every
// message, keeping the mapping error on rows that failed. Errors on messages
// without the collector_row marker failed before rows existed (the query).
func runPipeline(ctx context.Context, conf string) ([]Row, []string, error) {
	sb := service.NewStreamBuilder()
	if err := sb.SetYAML(conf); err != nil {
		return nil, nil, fmt.Errorf("rendered config is invalid: %w", err)
	}
	var mu sync.Mutex
	rows := []Row{}
	var queryErrs []string
	err := sb.AddConsumerFunc(func(_ context.Context, msg *service.Message) error {
		raw, _ := msg.AsBytes()
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
		return nil, nil, err
	}
	stream, err := sb.Build()
	if err != nil {
		return nil, nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := stream.Run(runCtx); err != nil && err != context.Canceled {
		return nil, nil, err
	}
	return rows, queryErrs, nil
}
