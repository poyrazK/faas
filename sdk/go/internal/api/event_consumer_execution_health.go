package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Execution counts describe retained invocations, including handler replays.
type EventConsumerExecutionHealth struct {
	LineageRetryJobs               *EventExecutionLineageRetryJobHealth `json:"lineage_retry_jobs,omitempty"`
	WindowCoverage                 string                               `json:"window_coverage,omitempty"`
	WindowHistoryComplete          bool                                 `json:"window_history_complete"`
	WindowHistoryStatus            string                               `json:"window_history_status,omitempty"`
	WindowHistoryReason            string                               `json:"window_history_reason,omitempty"`
	WindowCoverageStartedAt        *time.Time                           `json:"window_coverage_started_at,omitempty"`
	WindowCoverageLostThroughAt    *time.Time                           `json:"window_coverage_lost_through_at,omitempty"`
	WindowEvidenceRetainUntil      *time.Time                           `json:"window_evidence_retain_until,omitempty"`
	WindowEvidenceRetentionSeconds int64                                `json:"window_evidence_retention_seconds,omitempty"`
	CoverageStartedAt              *time.Time                           `json:"coverage_started_at,omitempty"`
	CoverageLostAt                 *time.Time                           `json:"coverage_lost_at,omitempty"`
	HistoryStatus                  string                               `json:"history_status"`
	HistoryReason                  string                               `json:"history_reason"`
	LatestSuccessfulCompletionAt   *time.Time                           `json:"latest_successful_completion_at,omitempty"`
	SubscriptionID                 string                               `json:"subscription_id"`
	AppID                          string                               `json:"app_id"`
	ObservedAt                     time.Time                            `json:"observed_at"`
	WindowStart                    time.Time                            `json:"window_start"`
	Coverage                       string                               `json:"coverage"`
	HistoryComplete                bool                                 `json:"history_complete"`
	Truncated                      bool                                 `json:"truncated"`
	RetainedRoots                  int64                                `json:"retained_roots"`
	MissingRoots                   int64                                `json:"missing_roots"`
	Executions                     int64                                `json:"executions"`
	Queued                         int64                                `json:"queued"`
	Running                        int64                                `json:"running"`
	Retrying                       int64                                `json:"retrying"`
	Succeeded                      int64                                `json:"succeeded"`
	Failed                         int64                                `json:"failed"`
	Expired                        int64                                `json:"expired"`
	DeadLettered                   int64                                `json:"dead_lettered"`
	Cancelled                      int64                                `json:"cancelled"`
	Superseded                     int64                                `json:"superseded"`
	Unknown                        int64                                `json:"unknown"`
	SuccessfulAttempts             int64                                `json:"successful_attempts"`
	FailedAttempts                 int64                                `json:"failed_attempts"`
	UnknownAttempts                int64                                `json:"unknown_attempts"`
	HandlerFailurePct              float64                              `json:"handler_failure_pct"`
	WindowCompletions              int64                                `json:"window_completions"`
	WindowDeadLetters              int64                                `json:"window_dead_letters"`
	DeadLetterRatePerSecond        float64                              `json:"dead_letter_rate_per_second"`
	CompletionLatencyP95Seconds    float64                              `json:"completion_latency_p95_seconds"`
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

type EventExecutionLineageCase struct {
	DiagnosticID     string     `json:"diagnostic_id,omitempty"`
	SubscriptionID   string     `json:"subscription_id"`
	AppID            string     `json:"app_id"`
	OutboxID         int64      `json:"outbox_id"`
	AcceptedAt       time.Time  `json:"accepted_at"`
	RootInvocationID string     `json:"root_invocation_id,omitempty"`
	RootCreatedAt    *time.Time `json:"root_created_at,omitempty"`
	Status           string     `json:"status"`
	Reason           string     `json:"reason"`
	Revision         int64      `json:"revision"`
	RetryCount       int64      `json:"retry_count"`
	RecordedAt       time.Time  `json:"recorded_at"`
	RetainUntil      time.Time  `json:"retain_until"`
}
type EventExecutionLineageCases struct {
	Cases     []EventExecutionLineageCase `json:"cases"`
	NextAfter int64                       `json:"next_after,omitempty"`
}
type RetryEventExecutionLineageCaseRequest struct {
	ExpectedDiagnosticID string `json:"expected_diagnostic_id,omitempty"`
	RequestID            string `json:"request_id"`
	ExpectedRevision     int64  `json:"expected_revision"`
}

func (c *Client) ListEventExecutionLineageCases(ctx context.Context, app, sub string, after int64, limit int) (EventExecutionLineageCases, error) {
	var out EventExecutionLineageCases
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/cases?after="+strconv.FormatInt(after, 10)+"&limit="+strconv.Itoa(limit), nil, &out)
	return out, err
}
func (c *Client) RetryEventExecutionLineageCase(ctx context.Context, app, sub string, id int64, req RetryEventExecutionLineageCaseRequest) (EventExecutionLineageCase, error) {
	var out EventExecutionLineageCase
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/cases/"+strconv.FormatInt(id, 10)+"/retry", req, &out)
	return out, err
}

// EventExecutionLineageRetrySelection fences both the case revision and original receipt acceptance.
type EventExecutionLineageRetrySelection struct {
	ExpectedDiagnosticID string    `json:"expected_diagnostic_id,omitempty"`
	OutboxID             int64     `json:"outbox_id"`
	ExpectedRevision     int64     `json:"expected_revision"`
	AcceptedAt           time.Time `json:"accepted_at"`
}
type CreateEventExecutionLineageRetryJobRequest struct {
	RequestID string                                `json:"request_id"`
	Cases     []EventExecutionLineageRetrySelection `json:"cases"`
}
type EventExecutionLineageRetryJobItem struct {
	Selection EventExecutionLineageRetrySelection `json:"selection"`
	Status    string                              `json:"status"`
	Reason    string                              `json:"reason,omitempty"`
	Result    *EventExecutionLineageCase          `json:"result,omitempty"`
}
type EventExecutionLineageRetryJob struct {
	LastProgressAt *time.Time                                   `json:"last_progress_at,omitempty"`
	ControlReceipt *EventExecutionLineageRetryJobControlReceipt `json:"control_receipt,omitempty"`
	RequestID      string                                       `json:"request_id"`
	SubscriptionID string                                       `json:"subscription_id"`
	AppID          string                                       `json:"app_id"`
	Status         string                                       `json:"status"`
	Processed      int                                          `json:"processed"`
	Items          []EventExecutionLineageRetryJobItem          `json:"items"`
	CreatedAt      time.Time                                    `json:"created_at"`
	UpdatedAt      time.Time                                    `json:"updated_at"`
	RetainUntil    time.Time                                    `json:"retain_until"`
}

func (c *Client) PreviewEventExecutionLineageRetries(ctx context.Context, app, sub, reason string, after int64, limit int) (EventExecutionLineageCases, error) {
	var out EventExecutionLineageCases
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-preview?after="+strconv.FormatInt(after, 10)+"&limit="+strconv.Itoa(limit)+"&reason="+url.QueryEscape(reason), nil, &out)
	return out, err
}
func (c *Client) CreateEventExecutionLineageRetryJob(ctx context.Context, app, sub string, req CreateEventExecutionLineageRetryJobRequest) (EventExecutionLineageRetryJob, error) {
	var out EventExecutionLineageRetryJob
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-jobs", req, &out)
	return out, err
}
func (c *Client) GetEventExecutionLineageRetryJob(ctx context.Context, app, sub, requestID string) (EventExecutionLineageRetryJob, error) {
	var out EventExecutionLineageRetryJob
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-jobs/"+url.PathEscape(requestID), nil, &out)
	return out, err
}

// CancelEventExecutionLineageRetryJobRequest fences the original job incarnation.
type CancelEventExecutionLineageRetryJobRequest struct {
	CommandID         string    `json:"command_id,omitempty"`
	ExpectedCreatedAt time.Time `json:"expected_created_at"`
}

func (c *Client) CancelEventExecutionLineageRetryJob(ctx context.Context, app, sub, requestID string, req CancelEventExecutionLineageRetryJobRequest) (EventExecutionLineageRetryJob, error) {
	var out EventExecutionLineageRetryJob
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-jobs/"+url.PathEscape(requestID)+"/cancel", req, &out)
	return out, err
}

// ControlEventExecutionLineageRetryJobRequest fences the original bulk job incarnation.
type ControlEventExecutionLineageRetryJobRequest struct {
	CommandID         string    `json:"command_id,omitempty"`
	ExpectedCreatedAt time.Time `json:"expected_created_at"`
}

func (c *Client) PauseEventExecutionLineageRetryJob(ctx context.Context, app, sub, id string, req ControlEventExecutionLineageRetryJobRequest) (EventExecutionLineageRetryJob, error) {
	return c.controlEventExecutionLineageRetryJob(ctx, app, sub, id, "pause", req)
}
func (c *Client) ResumeEventExecutionLineageRetryJob(ctx context.Context, app, sub, id string, req ControlEventExecutionLineageRetryJobRequest) (EventExecutionLineageRetryJob, error) {
	return c.controlEventExecutionLineageRetryJob(ctx, app, sub, id, "resume", req)
}
func (c *Client) controlEventExecutionLineageRetryJob(ctx context.Context, app, sub, id, action string, req ControlEventExecutionLineageRetryJobRequest) (EventExecutionLineageRetryJob, error) {
	var out EventExecutionLineageRetryJob
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-jobs/"+url.PathEscape(id)+"/"+action, req, &out)
	return out, err
}

// EventExecutionLineageRetryJobControlReceipt is the immutable outcome of a UUID-keyed control.
type EventExecutionLineageRetryJobControlReceipt struct {
	ActorKind         string    `json:"actor_kind,omitempty"`
	ActorID           string    `json:"actor_id,omitempty"`
	CredentialType    string    `json:"credential_type,omitempty"`
	CommandID         string    `json:"command_id"`
	Sequence          int64     `json:"sequence"`
	JobRequestID      string    `json:"job_request_id"`
	SubscriptionID    string    `json:"subscription_id"`
	AppID             string    `json:"app_id"`
	Action            string    `json:"action"`
	ExpectedCreatedAt time.Time `json:"expected_created_at"`
	ResultStatus      string    `json:"result_status"`
	Processed         int       `json:"processed"`
	Selected          int       `json:"selected"`
	JobUpdatedAt      time.Time `json:"job_updated_at"`
	RecordedAt        time.Time `json:"recorded_at"`
	RetainUntil       time.Time `json:"retain_until"`
}
type EventExecutionLineageRetryJobControls struct {
	Controls  []EventExecutionLineageRetryJobControlReceipt `json:"controls"`
	NextAfter int64                                         `json:"next_after,omitempty"`
}

// EventExecutionLineageRetryJobControlFilters combines exact history filters with AND.
type EventExecutionLineageRetryJobControlFilters struct {
	Action         string `json:"action,omitempty"`
	ActorID        string `json:"actor_id,omitempty"`
	CredentialType string `json:"credential_type,omitempty"`
}

func (f EventExecutionLineageRetryJobControlFilters) Normalize() (EventExecutionLineageRetryJobControlFilters, error) {
	switch f.Action {
	case "", "pause", "resume", "cancel":
	default:
		return f, fmt.Errorf("action must be pause, resume or cancel")
	}
	switch f.CredentialType {
	case "", "session", "api_key", "deploy_token", "platform_tenant_token", "internal":
	default:
		return f, fmt.Errorf("invalid credential_type")
	}
	if f.ActorID != "" && f.ActorID != "store" {
		id := strings.ToLower(f.ActorID)
		if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
			return f, fmt.Errorf("actor_id must be a nonzero hyphenated UUID or store")
		}
		compact := strings.ReplaceAll(id, "-", "")
		decoded, err := hex.DecodeString(compact)
		if err != nil || len(decoded) != 16 || compact == "00000000000000000000000000000000" {
			return f, fmt.Errorf("actor_id must be a nonzero hyphenated UUID or store")
		}
		f.ActorID = id
	}
	return f, nil
}

// An omitted filter preserves unfiltered history; supply at most one filter object.
func (c *Client) ListEventExecutionLineageRetryJobControls(ctx context.Context, app, sub, id string, expected time.Time, after int64, limit int, filters ...EventExecutionLineageRetryJobControlFilters) (EventExecutionLineageRetryJobControls, error) {
	var out EventExecutionLineageRetryJobControls
	if len(filters) > 1 {
		return out, fmt.Errorf("supply at most one history filter")
	}
	query := url.Values{"expected_created_at": {expected.Format(time.RFC3339Nano)}, "after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	if len(filters) == 1 {
		f, err := filters[0].Normalize()
		if err != nil {
			return out, err
		}
		for key, value := range map[string]string{"action": f.Action, "actor_id": f.ActorID, "credential_type": f.CredentialType} {
			if value != "" {
				query.Set(key, value)
			}
		}
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-jobs/"+url.PathEscape(id)+"/controls?"+query.Encode(), nil, &out)
	return out, err
}

// EventExecutionLineageRetryJobSummary omits case selections and individual results.
type EventExecutionLineageRetryJobSummary struct {
	LastProgressAt *time.Time                                 `json:"last_progress_at,omitempty"`
	Attention      *EventExecutionLineageRetryJobHealthSample `json:"attention,omitempty"`
	RequestID      string                                     `json:"request_id"`
	SubscriptionID string                                     `json:"subscription_id"`
	AppID          string                                     `json:"app_id"`
	Status         string                                     `json:"status"`
	Selected       int                                        `json:"selected"`
	Processed      int                                        `json:"processed"`
	CreatedAt      time.Time                                  `json:"created_at"`
	UpdatedAt      time.Time                                  `json:"updated_at"`
	RetainUntil    time.Time                                  `json:"retain_until"`
}
type EventExecutionLineageRetryJobs struct {
	Attention *EventExecutionLineageRetryJobAttentionPage `json:"attention,omitempty"`
	Jobs      []EventExecutionLineageRetryJobSummary      `json:"jobs"`
	NextAfter string                                      `json:"next_after,omitempty"`
}

type EventExecutionLineageRetryJobAttentionOptions struct {
	Attention string `json:"attention,omitempty"`
}

func (o EventExecutionLineageRetryJobAttentionOptions) Validate(status string) error {
	switch o.Attention {
	case "", "any", "stalled", "retention_risk":
	default:
		return fmt.Errorf("attention must be any, stalled or retention_risk")
	}
	if o.Attention != "" && status != "" && status != "pending" && status != "paused" {
		return fmt.Errorf("attention requires pending or paused status")
	}
	return nil
}

type EventExecutionLineageRetryJobAttentionPage struct {
	Coverage                 string    `json:"coverage"`
	Filter                   string    `json:"filter"`
	ObservedAt               time.Time `json:"observed_at"`
	HasMore                  bool      `json:"has_more"`
	InactivityWarningSeconds int64     `json:"inactivity_warning_seconds"`
	RetentionWarningSeconds  int64     `json:"retention_warning_seconds"`
}

func (c *Client) ListEventExecutionLineageRetryJobs(ctx context.Context, app, sub, status, after string, limit int, options ...EventExecutionLineageRetryJobAttentionOptions) (EventExecutionLineageRetryJobs, error) {
	var out EventExecutionLineageRetryJobs
	if len(options) > 1 {
		return out, fmt.Errorf("supply at most one attention option")
	}
	var option EventExecutionLineageRetryJobAttentionOptions
	if len(options) == 1 {
		option = options[0]
	}
	if err := option.Validate(status); err != nil {
		return out, err
	}
	query := url.Values{"status": {status}, "after": {after}, "limit": {strconv.Itoa(limit)}}
	if option.Attention != "" {
		query.Set("attention", option.Attention)
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/retry-jobs?"+query.Encode(), nil, &out)
	if err == nil && option.Attention != "" && (out.Attention == nil || out.Attention.Coverage != "live_matching_retry_jobs_page" || out.Attention.Filter != option.Attention || out.Attention.ObservedAt.IsZero() || out.Attention.HasMore != (out.NextAfter != "")) {
		return out, fmt.Errorf("server did not return compatible attention-page metadata")
	}
	return out, err
}

// EventExecutionLineageCaseBackfill reports the latest bounded diagnostic scan.
type EventExecutionLineageCaseBackfill struct {
	RequestID         string     `json:"request_id,omitempty"`
	PreviousRequestID string     `json:"previous_request_id,omitempty"`
	SubscriptionID    string     `json:"subscription_id"`
	AppID             string     `json:"app_id"`
	Status            string     `json:"status"`
	ThroughOutboxID   int64      `json:"through_outbox_id"`
	Cursor            int64      `json:"cursor"`
	Scanned           int64      `json:"scanned"`
	Recorded          int64      `json:"recorded"`
	Existing          int64      `json:"existing"`
	Associated        int64      `json:"associated"`
	Unresolved        int64      `json:"unresolved"`
	Conflicts         int64      `json:"conflicts"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	RetainUntil       *time.Time `json:"retain_until,omitempty"`
}
type RequestEventExecutionLineageCaseBackfillRequest struct {
	RequestID                 string `json:"request_id"`
	ExpectedPreviousRequestID string `json:"expected_previous_request_id,omitempty"`
}

func (c *Client) GetEventExecutionLineageCaseBackfill(ctx context.Context, app, sub string) (EventExecutionLineageCaseBackfill, error) {
	var out EventExecutionLineageCaseBackfill
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/case-backfill", nil, &out)
	return out, err
}
func (c *Client) RequestEventExecutionLineageCaseBackfill(ctx context.Context, app, sub string, req RequestEventExecutionLineageCaseBackfillRequest) (EventExecutionLineageCaseBackfill, error) {
	var out EventExecutionLineageCaseBackfill
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/case-backfill", req, &out)
	return out, err
}

// ControlEventExecutionLineageCaseBackfillRequest fences the original diagnostic scan incarnation.
type ControlEventExecutionLineageCaseBackfillRequest struct {
	ExpectedCreatedAt time.Time `json:"expected_created_at"`
}

func (c *Client) controlEventExecutionLineageCaseBackfill(ctx context.Context, app, sub, id, action string, req ControlEventExecutionLineageCaseBackfillRequest) (EventExecutionLineageCaseBackfill, error) {
	var out EventExecutionLineageCaseBackfill
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(sub)+"/execution-reconciliation/case-backfill/"+url.PathEscape(id)+"/"+action, req, &out)
	return out, err
}
func (c *Client) PauseEventExecutionLineageCaseBackfill(ctx context.Context, app, sub, id string, req ControlEventExecutionLineageCaseBackfillRequest) (EventExecutionLineageCaseBackfill, error) {
	return c.controlEventExecutionLineageCaseBackfill(ctx, app, sub, id, "pause", req)
}
func (c *Client) ResumeEventExecutionLineageCaseBackfill(ctx context.Context, app, sub, id string, req ControlEventExecutionLineageCaseBackfillRequest) (EventExecutionLineageCaseBackfill, error) {
	return c.controlEventExecutionLineageCaseBackfill(ctx, app, sub, id, "resume", req)
}
func (c *Client) CancelEventExecutionLineageCaseBackfill(ctx context.Context, app, sub, id string, req ControlEventExecutionLineageCaseBackfillRequest) (EventExecutionLineageCaseBackfill, error) {
	return c.controlEventExecutionLineageCaseBackfill(ctx, app, sub, id, "cancel", req)
}

// EventExecutionLineageRetryJobHealth observes retained pending/paused jobs independently of the execution window.
type EventExecutionLineageRetryJobHealth struct {
	Coverage                 string                                      `json:"coverage"`
	CountsComplete           bool                                        `json:"counts_complete"`
	JobLimit                 int                                         `json:"job_limit"`
	InactivityWarningSeconds int64                                       `json:"inactivity_warning_seconds"`
	RetentionWarningSeconds  int64                                       `json:"retention_warning_seconds"`
	ObservedJobs             int64                                       `json:"observed_jobs"`
	PendingJobs              int64                                       `json:"pending_jobs"`
	PausedJobs               int64                                       `json:"paused_jobs"`
	RemainingCases           int64                                       `json:"remaining_cases"`
	PendingRemainingCases    int64                                       `json:"pending_remaining_cases"`
	PausedRemainingCases     int64                                       `json:"paused_remaining_cases"`
	StalledPendingJobs       int64                                       `json:"stalled_pending_jobs"`
	RetentionRiskJobs        int64                                       `json:"retention_risk_jobs"`
	UnknownProgressJobs      int64                                       `json:"unknown_progress_jobs"`
	OldestPendingActivityAt  *time.Time                                  `json:"oldest_pending_activity_at,omitempty"`
	NextRetainUntil          *time.Time                                  `json:"next_retain_until,omitempty"`
	Jobs                     []EventExecutionLineageRetryJobHealthSample `json:"jobs"`
}
type EventExecutionLineageRetryJobHealthSample struct {
	RequestID            string     `json:"request_id"`
	Status               string     `json:"status"`
	Processed            int        `json:"processed"`
	Selected             int        `json:"selected"`
	Remaining            int        `json:"remaining"`
	CreatedAt            time.Time  `json:"created_at"`
	LastActivityAt       time.Time  `json:"last_activity_at"`
	InactivityAgeSeconds float64    `json:"inactivity_age_seconds"`
	LastProgressAt       *time.Time `json:"last_progress_at,omitempty"`
	ProgressAgeSeconds   *float64   `json:"progress_age_seconds,omitempty"`
	ProgressKnown        bool       `json:"progress_known"`
	StalledPending       bool       `json:"stalled_pending"`
	RetentionRisk        bool       `json:"retention_risk"`
	RetainUntil          time.Time  `json:"retain_until"`
	RetentionSeconds     float64    `json:"retention_seconds"`
}
