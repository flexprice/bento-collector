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

// Client uses the public API only: lookups for validation and bulk
// ingestion for sending.
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

// CustomerExists reports whether an external_customer_id is a Flexprice customer.
func (f Client) CustomerExists(ctx context.Context, externalID string) (bool, error) {
	code, err := f.do(ctx, http.MethodGet, "/v1/customers/external/"+url.PathEscape(externalID), nil, nil)
	if code == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

// MetersFor returns the ids of meters listening to an event name.
func (f Client) MetersFor(ctx context.Context, eventName string) ([]string, error) {
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
func (f Client) SendBulk(ctx context.Context, events []map[string]any) (int, error) {
	for i := 0; i < len(events); i += 1000 {
		j := min(i+1000, len(events))
		if _, err := f.do(ctx, http.MethodPost, "/v1/events/bulk", map[string]any{"events": events[i:j]}, nil); err != nil {
			return i, err
		}
	}
	return len(events), nil
}
