package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Execution counts describe retained invocations, including handler replays.
type EventConsumerExecutionHealth struct {
	SubscriptionID              string    `json:"subscription_id"`
	AppID                       string    `json:"app_id"`
	ObservedAt                  time.Time `json:"observed_at"`
	WindowStart                 time.Time `json:"window_start"`
	Coverage                    string    `json:"coverage"`
	HistoryComplete             bool      `json:"history_complete"`
	Truncated                   bool      `json:"truncated"`
	RetainedRoots               int64     `json:"retained_roots"`
	MissingRoots                int64     `json:"missing_roots"`
	Executions                  int64     `json:"executions"`
	Queued                      int64     `json:"queued"`
	Running                     int64     `json:"running"`
	Retrying                    int64     `json:"retrying"`
	Succeeded                   int64     `json:"succeeded"`
	Failed                      int64     `json:"failed"`
	Expired                     int64     `json:"expired"`
	DeadLettered                int64     `json:"dead_lettered"`
	Cancelled                   int64     `json:"cancelled"`
	Superseded                  int64     `json:"superseded"`
	Unknown                     int64     `json:"unknown"`
	SuccessfulAttempts          int64     `json:"successful_attempts"`
	FailedAttempts              int64     `json:"failed_attempts"`
	UnknownAttempts             int64     `json:"unknown_attempts"`
	HandlerFailurePct           float64   `json:"handler_failure_pct"`
	WindowCompletions           int64     `json:"window_completions"`
	WindowDeadLetters           int64     `json:"window_dead_letters"`
	DeadLetterRatePerSecond     float64   `json:"dead_letter_rate_per_second"`
	CompletionLatencyP95Seconds float64   `json:"completion_latency_p95_seconds"`
}

func (c *Client) GetEventConsumerExecutionHealth(ctx context.Context, app, id, window string) (EventConsumerExecutionHealth, error) {
	var out EventConsumerExecutionHealth
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/execution-health?window="+url.QueryEscape(window), nil, &out)
	return out, err
}
func IsEventConsumerExecutionAlertMetric(metric string) bool {
	switch metric {
	case "event_execution_dead_letters", "event_execution_dead_letter_rate_per_second", "event_handler_failure_pct", "event_completion_latency_p95_seconds":
		return true
	}
	return false
}
