package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EventReplayBackfillRequest creates a durable historical delivery job.
type EventReplayBackfillRequest struct {
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
}

func (r EventReplayBackfillRequest) Validate() error {
	if r.From.IsZero() || r.Until.IsZero() || !r.From.Before(r.Until) {
		return fmt.Errorf("from and until must define a nonempty acceptance-time range")
	}
	return nil
}

// EventReplayBackfillProgress reports durable per-envelope routing outcomes.
type EventReplayBackfillProgress struct {
	Scanned          int64 `json:"scanned"`
	Matched          int64 `json:"matched"`
	Filtered         int64 `json:"filtered"`
	Pending          int64 `json:"pending"`
	Processing       int64 `json:"processing"`
	Enqueued         int64 `json:"enqueued"`
	Failed           int64 `json:"failed"`
	RetryableFailed  int64 `json:"retryable_failed"`
	SkippedCaptured  int64 `json:"skipped_captured"`
	SkippedUnknown   int64 `json:"skipped_unknown"`
	SkippedExisting  int64 `json:"skipped_existing"`
	SkippedUnsettled int64 `json:"skipped_unsettled"`
}

type EventReplayBackfillJobResponse struct {
	ID                   string                      `json:"id"`
	AppSlug              string                      `json:"app_slug"`
	SubscriptionID       string                      `json:"subscription_id"`
	SubscriptionRevision string                      `json:"subscription_revision"`
	From                 time.Time                   `json:"from"`
	Until                time.Time                   `json:"until"`
	CutoffAt             time.Time                   `json:"cutoff_at"`
	EarliestRetainedAt   *time.Time                  `json:"earliest_retained_at,omitempty"`
	HistoryComplete      bool                        `json:"history_complete"`
	DuplicatePolicy      string                      `json:"duplicate_policy"`
	State                string                      `json:"state"`
	ScanComplete         bool                        `json:"scan_complete"`
	Progress             EventReplayBackfillProgress `json:"progress"`
	CreatedAt            time.Time                   `json:"created_at"`
	UpdatedAt            time.Time                   `json:"updated_at"`
	CompletedAt          *time.Time                  `json:"completed_at,omitempty"`
}

type EventReplayBackfillRetryResponse struct {
	RetriedCount            int64                          `json:"retried_count"`
	RemainingRetryableCount int64                          `json:"remaining_retryable_count"`
	Job                     EventReplayBackfillJobResponse `json:"job"`
}

type EventReplayBackfillRetryRequest struct {
	Limit *int `json:"limit,omitempty"`
}

// EventReplayBackfillItem is the metadata-only outcome for one envelope in a
// durable backfill. It remains useful after the source envelope is pruned.
type EventReplayBackfillItem struct {
	EventSource       string    `json:"event_source"`
	EventID           string    `json:"event_id"`
	EventType         string    `json:"event_type"`
	SchemaVersion     string    `json:"schema_version,omitempty"`
	AcceptedAt        time.Time `json:"accepted_at"`
	State             string    `json:"state"`
	Attempts          int       `json:"attempts"`
	FailureCode       string    `json:"failure_code,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	DetailsTruncated  bool      `json:"details_truncated,omitempty"`
	Retryable         bool      `json:"retryable"`
	UpdatedAt         time.Time `json:"updated_at"`
	ReceiptURL        string    `json:"receipt_url,omitempty"`
	AttemptHistoryURL string    `json:"attempt_history_url,omitempty"`
}

// EventReplayBackfillItemsResponse is one stable, acceptance-ordered page of
// per-envelope outcomes for a durable backfill job.
type EventReplayBackfillItemsResponse struct {
	JobID     string                    `json:"job_id"`
	Items     []EventReplayBackfillItem `json:"items"`
	NextAfter string                    `json:"next_after,omitempty"`
}

// EventReplayBackfillItemsQuery controls a bounded metadata-only item read.
type EventReplayBackfillItemsQuery struct {
	State string
	After string
	Limit int
}

func (c *Client) CreateEventReplayBackfill(ctx context.Context, appSlug, subscriptionID string, request EventReplayBackfillRequest) (EventReplayBackfillJobResponse, error) {
	var out EventReplayBackfillJobResponse
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(appSlug)+"/event-subscriptions/"+url.PathEscape(subscriptionID)+"/replays", request, &out)
	return out, err
}

func (c *Client) GetEventReplayBackfill(ctx context.Context, jobID string) (EventReplayBackfillJobResponse, error) {
	var out EventReplayBackfillJobResponse
	err := c.do(ctx, http.MethodGet, "/v1/event-replays/"+url.PathEscape(jobID), nil, &out)
	return out, err
}

func (c *Client) ListEventReplayBackfillItems(ctx context.Context, jobID, state, after string, limit int) (EventReplayBackfillItemsResponse, error) {
	var out EventReplayBackfillItemsResponse
	values := url.Values{}
	if state != "" {
		values.Set("state", state)
	}
	if after != "" {
		values.Set("after", after)
	}
	if limit > 0 {
		values.Set("limit", fmt.Sprint(limit))
	}
	path := "/v1/event-replays/" + url.PathEscape(jobID) + "/items"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) RetryFailedEventReplayBackfill(ctx context.Context, jobID string, limit int) (EventReplayBackfillRetryResponse, error) {
	var out EventReplayBackfillRetryResponse
	request := EventReplayBackfillRetryRequest{}
	if limit > 0 {
		request.Limit = &limit
	}
	err := c.do(ctx, http.MethodPost, "/v1/event-replays/"+url.PathEscape(jobID)+"/retry-failed", request, &out)
	return out, err
}
