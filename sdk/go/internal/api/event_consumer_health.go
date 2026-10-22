package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EventConsumerHealth measures routing to a consumer, before invocation execution.
// Rates describe retained recorded outcomes; HistoryCompacted marks incomplete windows.
type EventConsumerHealth struct {
	EventSubscriptionDeliveryControl
	ObservedAt               time.Time `json:"observed_at"`
	WindowStart              time.Time `json:"window_start"`
	Coverage                 string    `json:"coverage"`
	PausedSeconds            float64   `json:"paused_seconds"`
	RetryScheduled           int64     `json:"retry_scheduled"`
	SuccessfulRoutes         int64     `json:"successful_routes"`
	ExpiredDeliveries        int64     `json:"expired_deliveries"`
	TerminalFailures         int64     `json:"terminal_failures"`
	RetryRatePerSecond       float64   `json:"retry_rate_per_second"`
	TerminalFailurePct       float64   `json:"terminal_failure_pct"`
	RoutingLatencyP95Seconds float64   `json:"routing_latency_p95_seconds"`
	DrainRatePerSecond       float64   `json:"drain_rate_per_second"`
	HistoryCompacted         bool      `json:"history_compacted"`
}

func EventConsumerHealthWindow(spec string) (time.Duration, error) {
	switch spec {
	case "", "5m":
		return 5 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "6h":
		return 6 * time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("window must be 5m, 15m, 1h, 6h, or 24h")
}
func (c *Client) GetEventConsumerHealth(ctx context.Context, app, id, window string) (EventConsumerHealth, error) {
	var out EventConsumerHealth
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/health?window="+url.QueryEscape(window), nil, &out)
	return out, err
}
