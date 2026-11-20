package flexprice

import (
	"github.com/flexprice/bento-collector/collector/internal/httpx"
	"github.com/flexprice/bento-collector/collector/spec"

	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client uses the public API for checks only; the published Bento config sends.
type Client struct {
	HTTP   *http.Client
	Conn   spec.Connection
	APIKey string
}

func (f Client) do(ctx context.Context, method, path string, in, out any) (int, error) {
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
		return resp.StatusCode, fmt.Errorf("flexprice %s %s returned HTTP %d: %s", method, path, resp.StatusCode, httpx.Truncate(string(b), 300))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (f Client) Test(ctx context.Context) error {
	_, err := f.do(ctx, http.MethodGet, "/v1/meters?limit=1", nil, nil)
	return err
}

// Meter is a Flexprice meter as the collector needs it.
type Meter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EventName   string `json:"event_name"`
	Status      string `json:"status,omitempty"`
	Aggregation struct {
		Type       string `json:"type"`
		Field      string `json:"field,omitempty"`
		Expression string `json:"expression,omitempty"`
	} `json:"aggregation"`
	Filters []struct {
		Key    string   `json:"key"`
		Values []string `json:"values"`
	} `json:"filters"`
}

// ValueProperty is the event property the meter reads, or "" when it reads
// none (COUNT, or an expression).
func (m Meter) ValueProperty() string {
	if m.Aggregation.Expression != "" || m.Aggregation.Type == "COUNT" {
		return ""
	}
	return m.Aggregation.Field
}

// Meters lists the published meters.
func (f Client) Meters(ctx context.Context) ([]Meter, error) {
	out := []Meter{}
	for offset := 0; ; offset += 500 {
		var r struct {
			Items []Meter `json:"items"`
		}
		if _, err := f.do(ctx, http.MethodGet, fmt.Sprintf("/v1/meters?limit=500&offset=%d", offset), nil, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Items {
			if m.Status == "" || m.Status == "published" {
				out = append(out, m)
			}
		}
		if len(r.Items) < 500 {
			return out, nil
		}
	}
}

// MetersFor returns the published meters listening to an event name.
func (f Client) MetersFor(ctx context.Context, eventName string) ([]Meter, error) {
	var r struct {
		Items []Meter `json:"items"`
	}
	if _, err := f.do(ctx, http.MethodGet, "/v1/meters?limit=500&event_name="+url.QueryEscape(eventName), nil, &r); err != nil {
		return nil, err
	}
	var out []Meter
	for _, m := range r.Items {
		if m.EventName == eventName && (m.Status == "" || m.Status == "published") {
			out = append(out, m)
		}
	}
	return out, nil
}

// Customer is a Flexprice customer as the collector shows it.
type Customer struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}

// SearchCustomers finds customers whose name or external ID contains q.
func (f Client) SearchCustomers(ctx context.Context, q string, limit int) ([]Customer, error) {
	if strings.TrimSpace(q) == "" {
		r := struct {
			Items []Customer `json:"items"`
		}{Items: []Customer{}}
		_, err := f.do(ctx, http.MethodGet, fmt.Sprintf("/v1/customers?limit=%d", limit), nil, &r)
		return r.Items, err
	}
	seen := map[string]bool{}
	out := []Customer{}
	for _, field := range []string{"external_id", "name"} {
		body := map[string]any{"limit": limit, "filters": []map[string]any{{
			"field": field, "operator": "contains", "data_type": "string", "value": map[string]string{"string": q},
		}}}
		var r struct {
			Items []Customer `json:"items"`
		}
		if _, err := f.do(ctx, http.MethodPost, "/v1/customers/search", body, &r); err != nil {
			return nil, err
		}
		for _, c := range r.Items {
			if !seen[c.ExternalID] {
				seen[c.ExternalID] = true
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// ExistingCustomers returns which of the external IDs are Flexprice customers.
func (f Client) ExistingCustomers(ctx context.Context, externalIDs []string) (map[string]bool, error) {
	found := map[string]bool{}
	for i := 0; i < len(externalIDs); i += 100 {
		batch := externalIDs[i:min(i+100, len(externalIDs))]
		q := url.Values{"limit": {fmt.Sprint(len(batch))}}
		for _, id := range batch {
			q.Add("external_ids", id)
		}
		var r struct {
			Items []Customer `json:"items"`
		}
		if _, err := f.do(ctx, http.MethodGet, "/v1/customers?"+q.Encode(), nil, &r); err != nil {
			return nil, err
		}
		for _, c := range r.Items {
			found[c.ExternalID] = true
		}
	}
	return found, nil
}
