package promql

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Alert is the metadata for one active Prometheus alert. Callers must project
// labels and annotations explicitly before exposing them in an API response.
type Alert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    time.Time         `json:"activeAt"`
}

// Alerts reads rule annotations using the same deadline and response bounds as
// instant queries. It does not replace ALERTS_FOR_STATE as the activation clock.
func (c *Client) Alerts(ctx context.Context) ([]Alert, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("promql: client not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/alerts", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus alerts: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Alerts []Alert `json:"alerts"`
		} `json:"data"`
	}
	if err := decodePromQLResponse(resp.Body, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" || payload.Data.Alerts == nil {
		return nil, fmt.Errorf("prometheus alerts: invalid response")
	}
	if err := validatePromQLSeries(len(payload.Data.Alerts)); err != nil {
		return nil, err
	}
	return payload.Data.Alerts, nil
}
