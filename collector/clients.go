package collector

import (
	"bytes"
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

type PrometheusClient struct {
	HTTP   *http.Client
	Conn   Connection
	Secret string
}

func (p PrometheusClient) do(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
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
		return nil, fmt.Errorf("prometheus returned HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return b, nil
}

// Test checks reachability and credentials and returns the Prometheus version.
func (p PrometheusClient) Test(ctx context.Context) (string, error) {
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

func (p PrometheusClient) Query(ctx context.Context, query string, at time.Time) ([]Sample, error) {
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

// FlexpriceClient uses the public API only: lookups for validation and bulk
// ingestion for sending.
type FlexpriceClient struct {
	HTTP   *http.Client
	Conn   Connection
	APIKey string
}

func (f FlexpriceClient) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(f.Conn.URL, "/")+path, body)
	if err != nil {
		return 0, err
	}
	for k, v := range f.Conn.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("x-api-key", f.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp.StatusCode, fmt.Errorf("flexprice rejected the API key (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("flexprice %s %s returned HTTP %d: %s", method, path, resp.StatusCode, truncate(string(b), 300))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (f FlexpriceClient) Test(ctx context.Context) error {
	_, err := f.do(ctx, http.MethodGet, "/v1/meters?limit=1", nil, nil)
	return err
}

// CustomerExists reports whether an external_customer_id is a Flexprice customer.
func (f FlexpriceClient) CustomerExists(ctx context.Context, externalID string) (bool, error) {
	code, err := f.do(ctx, http.MethodGet, "/v1/customers/external/"+url.PathEscape(externalID), nil, nil)
	if code == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

// MetersFor returns the ids of meters listening to an event name.
func (f FlexpriceClient) MetersFor(ctx context.Context, eventName string) ([]string, error) {
	var r struct {
		Items []struct {
			ID        string `json:"id"`
			EventName string `json:"event_name"`
			Status    string `json:"status"`
		} `json:"items"`
	}
	if _, err := f.do(ctx, http.MethodGet, "/v1/meters?limit=500&event_name="+url.QueryEscape(eventName), nil, &r); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range r.Items {
		if m.EventName == eventName && (m.Status == "" || m.Status == "published") {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// SendBulk posts events in chunks of 1000 and returns how many were accepted.
func (f FlexpriceClient) SendBulk(ctx context.Context, events []map[string]any) (int, error) {
	for i := 0; i < len(events); i += 1000 {
		j := min(i+1000, len(events))
		if _, err := f.do(ctx, http.MethodPost, "/v1/events/bulk", map[string]any{"events": events[i:j]}, nil); err != nil {
			return i, err
		}
	}
	return len(events), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
