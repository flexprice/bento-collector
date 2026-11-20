// Package source defines the input providers a collector reads usage from.
// A provider turns a collector's source into Bento stages that emit one
// message per row; everything after that (mapping, checks, sending) is shared.
package source

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Conn is a connection with its secret resolved, ready for live calls.
type Conn struct {
	Connection spec.Connection
	Secret     string
	HTTP       *http.Client
}

// Input is one collector and window as a provider sees it. Secret is the real
// value for a live run, or an ${ENV} placeholder for an exported config.
type Input struct {
	Collector spec.Collector
	Conn      spec.Connection
	Secret    string
	Window    spec.Window
	Now       time.Time
}

// Stages fetch one window's rows. They start from a message whose metadata
// holds the window (window_start, window_end, window_end_unix, usage_date,
// now_unix), must set meta query (the query as sent; part of every event_id)
// and may set meta evaluated_at (unix seconds every row's "at" must equal).
// After Fetch every message is one raw row; Normalize (Bloblang) turns it
// into the shared row shape:
//
//	{"labels": {"k": "v", ...}, "value": "<number as a string>", "at": <unix seconds, optional>}
type Stages struct {
	Fetch     []any
	Normalize string
	// Query and EvaluatedAt are this window's values, for run results.
	Query       string
	EvaluatedAt time.Time
}

type Provider interface {
	Type() string
	ValidateConnection(spec.Connection) error
	ValidateSource(spec.Source) error
	// Test checks reachability and credentials and returns a short description.
	Test(ctx context.Context, c Conn) (string, error)
	Stages(in Input) (Stages, error)
	// Gate returns processors that evaluate the completeness query and set
	// meta collector_complete (bool) and collector_completeness (the values).
	Gate(in Input, cp spec.Completeness) ([]any, error)
	// Sample runs the source query for one window and returns its rows as
	// they come back, so the UI can offer real labels and values.
	Sample(ctx context.Context, c Conn, in Input) ([]Sample, error)
}

// Sample is one row of a source query.
type Sample struct {
	Labels map[string]string `json:"labels"`
	Value  string            `json:"value"`
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(ps ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range ps {
		r.providers[p.Type()] = p
	}
	return r
}

func (r *Registry) Get(typ string) (Provider, error) {
	if p, ok := r.providers[typ]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("unknown source type %q (supported: %v)", typ, r.Types())
}

func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.providers))
	for t := range r.providers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
