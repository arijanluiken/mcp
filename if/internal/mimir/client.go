package mimir

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Minimal client for Prometheus-compatible HTTP API

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
	return &Client{BaseURL: baseURL, HTTPClient: &http.Client{Timeout: 15 * time.Second}}
}

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

// Series queries the /api/v1/series endpoint with matchers over a time range.
func (c *Client) Series(ctx context.Context, matchers []string, start, end time.Time) (json.RawMessage, error) {
	endpoint := c.BaseURL + "/api/v1/series"
	q := url.Values{}
	for _, m := range matchers {
		q.Add("match[]", m)
	}
	q.Set("start", fmt.Sprintf("%d", start.Unix()))
	q.Set("end", fmt.Sprintf("%d", end.Unix()))
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
		return nil, fmt.Errorf("mimir series failed: %s", resp.Status)
	}
	var qr queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, err
	}
	if qr.Status != "success" {
		if qr.Error != "" {
			return nil, fmt.Errorf(qr.Error)
		}
		return nil, fmt.Errorf("series failed")
	}
	return qr.Data, nil
}
