package api

import (
	"context"
	"net/url"
	"time"
)

// CustomMetricSeriesSteps maps each supported history window (ADR-745) to its
// Prometheus step, keeping every response at roughly 60–180 points. 15d is
// the platform Prometheus retention.
var CustomMetricSeriesSteps = map[string]string{
	"1h": "1m", "6h": "5m", "24h": "15m", "7d": "1h", "15d": "2h",
}

// CustomMetricSeriesResponse is GET /v1/apps/{slug}/custom-metrics/{name}/series.
// Source follows the metrics contract: "prometheus", or "degraded: <reason>"
// with no points. A gap in Points means no fresh value was pushed then.
type CustomMetricSeriesResponse struct {
	AppID  string                    `json:"app_id"`
	Name   string                    `json:"name"`
	Range  string                    `json:"range"`
	Step   string                    `json:"step"`
	Source string                    `json:"source"`
	Points []CustomMetricSeriesPoint `json:"points"`
}

// CustomMetricSeriesPoint is one sampled value.
type CustomMetricSeriesPoint struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// GetCustomMetricSeries returns one custom metric's history. An empty rng
// selects CustomMetricSeriesDefaultRange.
func (c *Client) GetCustomMetricSeries(ctx context.Context, slug, name, rng string) (CustomMetricSeriesResponse, error) {
	var out CustomMetricSeriesResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/custom-metrics/" + url.PathEscape(name) + "/series"
	if rng != "" {
		path += "?" + url.Values{"range": {rng}}.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}
