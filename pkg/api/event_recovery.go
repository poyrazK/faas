package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type EventRecoveryRequest struct {
	ProtectReceipts     bool   `json:"protect_receipts,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Mode                string `json:"mode,omitempty"`
	Outcome             string `json:"outcome,omitempty"`
	SubscriptionID      string `json:"subscription_id,omitempty"`
	EventSource         string `json:"event_source,omitempty"`
	EventType           string `json:"event_type,omitempty"`
	FailureCode         string `json:"failure_code,omitempty"`
	MinAgeSeconds       int64  `json:"min_age_seconds,omitempty"`
	IncludeNonRetryable bool   `json:"include_non_retryable,omitempty"`
	RatePerSecond       int    `json:"rate_per_second,omitempty"`
}

func (r EventRecoveryRequest) Validate() error {
	if err := (EventRecoveryControlRequest{Reason: r.Reason}).Validate(); err != nil {
		return err
	}
	if r.Mode != "" && r.Mode != "routing" && r.Mode != "execution" {
		return fmt.Errorf("mode must be routing or execution")
	}
	if r.Mode == "execution" {
		if r.Outcome != "" && r.Outcome != "failed" && r.Outcome != "dead_letter" {
			return fmt.Errorf("outcome must be failed or dead_letter")
		}
		if r.FailureCode != "" || r.IncludeNonRetryable {
			return fmt.Errorf("execution mode uses outcome, not routing failure filters")
		}
	} else if r.Outcome != "" {
		return fmt.Errorf("outcome requires execution mode")
	}

	for _, value := range []string{r.SubscriptionID, r.EventSource, r.EventType, r.FailureCode} {
		if len(value) > EventBacklogFilterMaxBytes {
			return fmt.Errorf("recovery filters must be at most %d bytes", EventBacklogFilterMaxBytes)
		}
	}
	if r.MinAgeSeconds < 0 || r.MinAgeSeconds > EventBacklogMinAgeMaxSeconds {
		return fmt.Errorf("min_age_seconds must be between 0 and %d", EventBacklogMinAgeMaxSeconds)
	}
	if r.RatePerSecond < 0 || r.RatePerSecond > EventRecoveryRateMax {
		return fmt.Errorf("rate_per_second must be between 0 and %d; zero uses the default", EventRecoveryRateMax)
	}
	return nil
}

type EventRecoveryExecution struct {
	RecordedAt     *time.Time `json:"recorded_at,omitempty"`
	EvidenceSource string     `json:"evidence_source,omitempty"`
	ObservedAt     time.Time  `json:"observed_at"`
	State          string     `json:"state"`
	Source         string     `json:"source"`
	Attempts       int        `json:"attempts"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}
type EventRecoveryExecutionSummary struct {
	SavedResults int64     `json:"saved_results"`
	ObservedAt   time.Time `json:"observed_at"`
	TrackedCount int64     `json:"tracked_count"`
	Queued       int64     `json:"queued"`
	Running      int64     `json:"running"`
	Retrying     int64     `json:"retrying"`
	Succeeded    int64     `json:"succeeded"`
	Failed       int64     `json:"failed"`
	DeadLettered int64     `json:"dead_lettered"`
	Expired      int64     `json:"expired"`
	Cancelled    int64     `json:"cancelled"`
	Superseded   int64     `json:"superseded"`
	Unknown      int64     `json:"unknown"`
}
type EventRecoveryItem struct {
	ReplayInvocationID string                  `json:"replay_invocation_id,omitempty"`
	ReplayGeneration   *int64                  `json:"replay_generation,omitempty"`
	Execution          *EventRecoveryExecution `json:"execution,omitempty"`
	InvocationID       string                  `json:"invocation_id,omitempty"`
	Position           int64                   `json:"position"`
	EventSource        string                  `json:"event_source"`
	EventID            string                  `json:"event_id"`
	EventType          string                  `json:"event_type"`
	SubscriptionID     string                  `json:"subscription_id"`
	FailedAt           time.Time               `json:"failed_at"`
	FailureCode        string                  `json:"failure_code"`
	Retryable          bool                    `json:"retryable"`
	State              string                  `json:"state"`
	Reason             string                  `json:"reason,omitempty"`
}
type EventRecoveryPreview struct {
	ObservedAt      time.Time           `json:"observed_at"`
	Coverage        string              `json:"coverage"`
	MatchedCount    int64               `json:"matched_count"`
	ExceedsJobLimit bool                `json:"exceeds_job_limit"`
	Sample          []EventRecoveryItem `json:"sample"`
}
type EventRecoveryJob struct {
	ExecutionFinishedAt *time.Time `json:"execution_finished_at,omitempty"`

	RatePerSecond  int                            `json:"rate_per_second"`
	PausedAt       *time.Time                     `json:"paused_at,omitempty"`
	Execution      *EventRecoveryExecutionSummary `json:"execution,omitempty"`
	ID             string                         `json:"id"`
	AppID          string                         `json:"app_id"`
	Coverage       string                         `json:"coverage"`
	Selection      EventRecoveryRequest           `json:"selection"`
	State          string                         `json:"state"`
	SelectedCount  int64                          `json:"selected_count"`
	PendingCount   int64                          `json:"pending_count"`
	QueuedCount    int64                          `json:"queued_count"`
	SkippedCount   int64                          `json:"skipped_count"`
	CancelledCount int64                          `json:"cancelled_count"`
	CreatedAt      time.Time                      `json:"created_at"`
	UpdatedAt      time.Time                      `json:"updated_at"`
	ExpiresAt      time.Time                      `json:"expires_at"`
	CompletedAt    *time.Time                     `json:"completed_at,omitempty"`
}
type EventRecoveryItems struct {
	JobID     string              `json:"job_id"`
	Items     []EventRecoveryItem `json:"items"`
	NextAfter int64               `json:"next_after,omitempty"`
}

func (c *Client) PreviewEventRecovery(ctx context.Context, app string, req EventRecoveryRequest) (EventRecoveryPreview, error) {
	var out EventRecoveryPreview
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries/preview", req, &out)
	return out, err
}
func (c *Client) CreateEventRecovery(ctx context.Context, app string, req EventRecoveryRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries", req, &out)
	return out, err
}
func (c *Client) GetEventRecovery(ctx context.Context, id string) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) CancelEventRecovery(ctx context.Context, id string) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/cancel", nil, &out)
	return out, err
}
func (c *Client) ListEventRecoveryItems(ctx context.Context, id string, after int64, limit int) (EventRecoveryItems, error) {
	var out EventRecoveryItems
	query := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id)+"/items?"+query.Encode(), nil, &out)
	return out, err
}

type EventRecoveryRateRequest struct {
	Reason        string `json:"reason,omitempty"`
	RatePerSecond int    `json:"rate_per_second"`
}

func (r EventRecoveryRateRequest) Validate() error {
	if err := (EventRecoveryControlRequest{Reason: r.Reason}).Validate(); err != nil {
		return err
	}
	if r.RatePerSecond < 1 || r.RatePerSecond > EventRecoveryRateMax {
		return fmt.Errorf("rate_per_second must be between 1 and %d", EventRecoveryRateMax)
	}
	return nil
}
func (c *Client) PauseEventRecovery(ctx context.Context, id string) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/pause", nil, &out)
	return out, err
}
func (c *Client) ResumeEventRecovery(ctx context.Context, id string) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/resume", nil, &out)
	return out, err
}
func (c *Client) SetEventRecoveryRate(ctx context.Context, id string, req EventRecoveryRateRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPut, "/v1/event-recoveries/"+url.PathEscape(id)+"/rate", req, &out)
	return out, err
}
