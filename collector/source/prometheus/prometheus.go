// Package prometheus reads usage with instant PromQL queries.
package prometheus

import (
	"github.com/flexprice/bento-collector/collector/internal/blobl"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/spec"

	"context"
	"fmt"
	"strings"
	"time"
)

const Type = "prometheus"

type Provider struct{}

func (Provider) Type() string { return Type }

func (Provider) ValidateConnection(c spec.Connection) error {
	switch c.AuthType {
	case "", "none", "basic", "bearer":
		return nil
	}
	return fmt.Errorf("auth_type %q is not supported for Prometheus; use none, basic or bearer", c.AuthType)
}

func (Provider) ValidateSource(spec.Source) error { return nil }

func (Provider) Test(ctx context.Context, c source.Conn) (string, error) {
	v, err := client(c).Test(ctx)
	if err != nil {
		return "", err
	}
	return "Connected to Prometheus " + v, nil
}

func (Provider) Values(ctx context.Context, c source.Conn, query string, at time.Time) ([]float64, error) {
	samples, err := client(c).Query(ctx, query, at)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.Value
	}
	return out, nil
}

// evalTime: window mode evaluates [1d] ranges at the window end; usage_date
// queries select the day themselves and are evaluated now.
func evalTime(in source.Input) time.Time {
	if in.Collector.Source.Mode == spec.ModeUsageDate {
		return in.Now.UTC().Truncate(time.Second)
	}
	return in.Window.End
}

func (Provider) Stages(in source.Input) (source.Stages, error) {
	src := in.Collector.Source
	at := "@window_end_unix"
	if src.Mode == spec.ModeUsageDate {
		at = "@now_unix"
	}
	build := fmt.Sprintf(`let q = %s.replace_all(%s, @usage_date)
let at = %s
meta query = $q
meta evaluated_at = $at
root = %s`, blobl.Quote(src.Query), blobl.Quote(spec.UsageDatePlaceholder), at, formBody("$q", "$at"))
	return source.Stages{
		Fetch: []any{
			map[string]any{"label": "prometheus_request", "mapping": build},
			map[string]any{"label": "prometheus_query", "http": httpProc(in)},
			map[string]any{"label": "prometheus_result", "mapping": responseMapping},
			map[string]any{"unarchive": map[string]any{"format": "json_array"}},
		},
		Normalize:   `root = {"labels": this.metric.or({}), "value": this.value.index(1).string(), "at": this.value.index(0).number()}`,
		Query:       src.QueryFor(in.Window),
		EvaluatedAt: evalTime(in),
	}, nil
}

func (Provider) Gate(in source.Input, cp spec.Completeness) ([]any, error) {
	return []any{map[string]any{"label": "completeness", "branch": map[string]any{
		"request_map": "root = " + formBody(blobl.Quote(cp.Query), "@window_end_unix"),
		"processors":  []any{map[string]any{"http": httpProc(in)}},
		"result_map":  fmt.Sprintf(`meta collector_complete = this.status == "success" && this.data.result.or([]).length() > 0 && this.data.result.all(s -> s.value.index(1).number().catch(0) >= %v)`, cp.Min),
	}}}, nil
}

// formBody is the Bloblang for an instant-query form body.
func formBody(query, unix string) string {
	return fmt.Sprintf(`"query=" + %s.escape_url_query() + "&time=" + %s.ts_format("2006-01-02T15:04:05Z07:00", "UTC").escape_url_query()`, query, unix)
}

func httpProc(in source.Input) map[string]any {
	headers := map[string]any{"Content-Type": "application/x-www-form-urlencoded"}
	for k, v := range in.Conn.Headers {
		headers[k] = v
	}
	p := map[string]any{
		"url":     strings.TrimRight(in.Conn.URL, "/") + "/api/v1/query",
		"verb":    "POST",
		"headers": headers,
		"timeout": "30s",
		"retries": 3,
	}
	switch in.Conn.AuthType {
	case "bearer":
		headers["Authorization"] = "Bearer " + in.Secret
	case "basic":
		p["basic_auth"] = map[string]any{"enabled": true, "username": in.Conn.Username, "password": in.Secret}
	}
	return p
}

const responseMapping = `root = if this.status != "success" {
  throw("Prometheus query failed: " + this.error.or("unknown error"))
} else if this.data.resultType != "vector" {
  throw("Expected an instant vector, got " + this.data.resultType.string())
} else if this.warnings.or([]).length() > 0 {
  throw("Prometheus returned warnings, usage may be partial: " + this.warnings.join("; "))
} else { this.data.result }
`

func client(c source.Conn) Client {
	return Client{HTTP: c.HTTP, Conn: c.Connection, Secret: c.Secret}
}
