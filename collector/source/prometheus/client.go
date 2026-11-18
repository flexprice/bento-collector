package prometheus

import (
	"github.com/flexprice/bento-collector/collector/internal/httpx"
	"github.com/flexprice/bento-collector/collector/spec"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sample is one series of a Prometheus instant vector.
type Sample struct {
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

// Client talks to the Prometheus HTTP API.
type Client struct {
	HTTP   *http.Client
	Conn   spec.Connection
	Secret string
}

func (p Client) do(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.Conn.URL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range p.Conn.Headers {
		req.Header.Set(k, v)
	}
	switch p.Conn.AuthType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+p.Secret)
	case "basic":
		req.SetBasicAuth(p.Conn.Username, p.Secret)
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("prometheus rejected the credentials (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		return nil, fmt.Errorf("prometheus returned HTTP %d: %s", resp.StatusCode, httpx.Truncate(string(b), 200))
	}
	return b, nil
}

// Test checks reachability and credentials and returns the Prometheus version.
func (p Client) Test(ctx context.Context) (string, error) {
	b, err := p.do(ctx, http.MethodGet, "/api/v1/status/buildinfo", nil)
	if err != nil {
		return "", err
	}
	var r struct {
		Status string `json:"status"`
		Data   struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Status != "success" {
		return "", fmt.Errorf("%s does not look like a Prometheus API", p.Conn.URL)
	}
	if _, err := p.Query(ctx, "vector(1)", time.Now()); err != nil {
		return "", err
	}
	return r.Data.Version, nil
}

func (p Client) Query(ctx context.Context, query string, at time.Time) ([]Sample, error) {
	form := url.Values{"query": {query}, "time": {at.UTC().Format(time.RFC3339)}}
	b, err := p.do(ctx, http.MethodPost, "/api/v1/query", form)
	if err != nil {
		return nil, err
	}
	var r struct {
		Status   string   `json:"status"`
		Error    string   `json:"error"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("bad prometheus response: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("query failed: %s", r.Error)
	}
	if len(r.Warnings) > 0 {
		return nil, fmt.Errorf("prometheus returned warnings, the result may be partial: %s", strings.Join(r.Warnings, "; "))
	}
	switch r.Data.ResultType {
	case "scalar":
		var v [2]any
		if err := json.Unmarshal(r.Data.Result, &v); err != nil {
			return nil, err
		}
		f, err := parseValue(v[1])
		return []Sample{{Value: f}}, err
	case "vector":
		var vec []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(r.Data.Result, &vec); err != nil {
			return nil, err
		}
		out := make([]Sample, 0, len(vec))
		for _, s := range vec {
			f, err := parseValue(s.Value[1])
			if err != nil {
				return nil, err
			}
			out = append(out, Sample{Labels: s.Metric, Value: f})
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected a scalar or instant vector, got %s", r.Data.ResultType)
}

func parseValue(v any) (float64, error) {
	s, _ := v.(string)
	return strconv.ParseFloat(s, 64)
}
