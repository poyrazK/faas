package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Execution counts describe retained invocations, including handler replays.
type EventConsumerExecutionHealth struct {
	WindowCoverage                 string     `json:"window_coverage,omitempty"`
	WindowHistoryComplete          bool       `json:"window_history_complete"`
	WindowHistoryStatus            string     `json:"window_history_status,omitempty"`
	WindowHistoryReason            string     `json:"window_history_reason,omitempty"`
	WindowCoverageStartedAt        *time.Time `json:"window_coverage_started_at,omitempty"`
	WindowCoverageLostThroughAt    *time.Time `json:"window_coverage_lost_through_at,omitempty"`
	WindowEvidenceRetainUntil      *time.Time `json:"window_evidence_retain_until,omitempty"`
	WindowEvidenceRetentionSeconds int64      `json:"window_evidence_retention_seconds,omitempty"`
	CoverageStartedAt              *time.Time `json:"coverage_started_at,omitempty"`
	CoverageLostAt                 *time.Time `json:"coverage_lost_at,omitempty"`
	HistoryStatus                  string     `json:"history_status"`
	HistoryReason                  string     `json:"history_reason"`
	LatestSuccessfulCompletionAt   *time.Time `json:"latest_successful_completion_at,omitempty"`
	SubscriptionID                 string     `json:"subscription_id"`
	AppID                          string     `json:"app_id"`
	ObservedAt                     time.Time  `json:"observed_at"`
	WindowStart                    time.Time  `json:"window_start"`
	Coverage                       string     `json:"coverage"`
	HistoryComplete                bool       `json:"history_complete"`
	Truncated                      bool       `json:"truncated"`
	RetainedRoots                  int64      `json:"retained_roots"`
	MissingRoots                   int64      `json:"missing_roots"`
	Executions                     int64      `json:"executions"`
	Queued                         int64      `json:"queued"`
	Running                        int64      `json:"running"`
	Retrying                       int64      `json:"retrying"`
	Succeeded                      int64      `json:"succeeded"`
	Failed                         int64      `json:"failed"`
	Expired                        int64      `json:"expired"`
	DeadLettered                   int64      `json:"dead_lettered"`
	Cancelled                      int64      `json:"cancelled"`
	Superseded                     int64      `json:"superseded"`
	Unknown                        int64      `json:"unknown"`
	SuccessfulAttempts             int64      `json:"successful_attempts"`
	FailedAttempts                 int64      `json:"failed_attempts"`
	UnknownAttempts                int64      `json:"unknown_attempts"`
	HandlerFailurePct              float64    `json:"handler_failure_pct"`
	WindowCompletions              int64      `json:"window_completions"`
	WindowDeadLetters              int64      `json:"window_dead_letters"`
	DeadLetterRatePerSecond        float64    `json:"dead_letter_rate_per_second"`
	CompletionLatencyP95Seconds    float64    `json:"completion_latency_p95_seconds"`
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

// CompleteWindowEvidence separates durable metric windows from current invocation state.
func (h EventConsumerExecutionHealth) CompleteWindowEvidence() bool {
	if h.WindowCoverage != "" {
		return h.WindowCoverage == "durable_execution_outcomes" && h.WindowHistoryComplete && h.WindowHistoryStatus == "complete" && h.UnknownAttempts == 0 && h.WindowCoverageStartedAt != nil && !h.WindowStart.Before(*h.WindowCoverageStartedAt) && (h.WindowCoverageLostThroughAt == nil || h.WindowStart.After(*h.WindowCoverageLostThroughAt)) && (h.WindowEvidenceRetainUntil == nil || h.WindowEvidenceRetainUntil.After(h.ObservedAt))
	}
	return h.Coverage == "bounded_retained_execution_roots" && h.HistoryComplete && !h.Truncated && h.MissingRoots == 0 && h.Unknown == 0 && h.UnknownAttempts == 0 && h.RetainedRoots > 0
}

// EventExecutionReconciliation reports a durable forward-only coverage request.
type EventExecutionReconciliation struct {
	RepairScanned                int64      `json:"repair_scanned"`
	RepairRepaired               int64      `json:"repair_repaired"`
	RepairUnresolved             int64      `json:"repair_unresolved"`
	RepairConflicts              int64      `json:"repair_conflicts"`
	SubscriptionID               string     `json:"subscription_id"`
	AppID                        string     `json:"app_id"`
	Status                       string     `json:"status"`
	RequestedAt                  *time.Time `json:"requested_at,omitempty"`
	CollectionStartedAt          *time.Time `json:"collection_started_at,omitempty"`
	UnattributedExecutionPresent bool       `json:"unattributed_execution_present"`
}

func (c *Client) GetEventExecutionReconciliation(ctx context.Context, app, id string) (EventExecutionReconciliation, error) {
	var out EventExecutionReconciliation
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/execution-reconciliation", nil, &out)
	return out, err
}
func (c *Client) RequestEventExecutionReconciliation(ctx context.Context, app, id string) (EventExecutionReconciliation, error) {
	var out EventExecutionReconciliation
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/execution-reconciliation", nil, &out)
	return out, err
}
