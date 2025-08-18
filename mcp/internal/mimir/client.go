package mimir

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Client is a tiny Mimir/Prometheus HTTP API client focused on query endpoints.
// It targets the Prometheus-compatible HTTP API exposed by Mimir.
// BaseURL should include the /prometheus prefix (e.g. http://mimir:9009/prometheus).

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type queryResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Query runs an instant query.
func (c *Client) Query(ctx context.Context, promQL string, ts time.Time) (json.RawMessage, error) {
	endpoint := c.BaseURL + "/api/v1/query"
	q := url.Values{}
	q.Set("query", promQL)
	if !ts.IsZero() {
		q.Set("time", fmt.Sprintf("%d", ts.Unix()))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("mimir query failed: %s", resp.Status)
	}
	var qr queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, err
	}
	if qr.Status != "success" {
		if qr.Error != "" {
			return nil, fmt.Errorf(qr.Error)
		}
		return nil, fmt.Errorf("query failed")
	}
	return qr.Data, nil
}

// QueryRange runs a range query.
func (c *Client) QueryRange(ctx context.Context, promQL string, start, end time.Time, step time.Duration) (json.RawMessage, error) {
	endpoint := c.BaseURL + "/api/v1/query_range"
	q := url.Values{}
	q.Set("query", promQL)
	q.Set("start", fmt.Sprintf("%d", start.Unix()))
	q.Set("end", fmt.Sprintf("%d", end.Unix()))
	q.Set("step", fmt.Sprintf("%ds", int(step.Seconds())))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("mimir query_range failed: %s", resp.Status)
	}
	var qr queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, err
	}
	if qr.Status != "success" {
		if qr.Error != "" {
			return nil, fmt.Errorf(qr.Error)
		}
		return nil, fmt.Errorf("query_range failed")
	}
	return qr.Data, nil
}
