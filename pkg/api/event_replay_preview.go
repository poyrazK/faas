package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// EventReplayPreviewOptions selects retained events by platform acceptance time,
// using [From, Until). After is opaque and must be passed back unchanged with
// the same range and target. Limit bounds envelopes examined, not just matches.
type EventReplayPreviewOptions struct {
	From  time.Time
	Until time.Time
	After string
	Limit int
}

func (o EventReplayPreviewOptions) Validate() error {
	if o.From.IsZero() || o.Until.IsZero() || !o.From.Before(o.Until) {
		return fmt.Errorf("from and until must define a nonempty acceptance-time range")
	}
	if o.Limit < 0 || o.Limit > EventReplayPreviewPageMax {
		return fmt.Errorf("limit must be between 1 and %d when supplied", EventReplayPreviewPageMax)
	}
	if len(o.After) > EventReplayPreviewCursorMaxBytes {
		return fmt.Errorf("after must be at most %d bytes", EventReplayPreviewCursorMaxBytes)
	}
	return nil
}

type EventReplayPreviewMatch struct {
	DeliveryExpired    bool       `json:"delivery_expired"`
	DeliveryDeadlineAt *time.Time `json:"delivery_deadline_at,omitempty"`
	EventID            string     `json:"event_id"`
	EventSource        string     `json:"event_source"`
	EventType          string     `json:"event_type"`
	SchemaVersion      string     `json:"schema_version,omitempty"`
	AcceptedAt         time.Time  `json:"accepted_at"`
	OriginalRecipient  string     `json:"original_recipient"` // captured, not_captured, unknown
	ReceiptURL         string     `json:"receipt_url"`
}

type EventReplayPreviewRetention struct {
	SettledRetentionSeconds int64      `json:"settled_retention_seconds"`
	EarliestRetainedAt      *time.Time `json:"earliest_retained_at,omitempty"` // account-wide
	HistoryComplete         bool       `json:"history_complete"`               // always false: no archive guarantee
}

type EventReplayPreviewResponse struct {
	SchemaVersionMismatchCount int                         `json:"schema_version_mismatch_count"`
	AppSlug                    string                      `json:"app_slug"`
	Subscription               EventSubscriptionResponse   `json:"subscription"`
	SubscriptionRevision       string                      `json:"subscription_revision"`
	From                       time.Time                   `json:"from"`
	Until                      time.Time                   `json:"until"`
	CutoffAt                   time.Time                   `json:"cutoff_at"`
	ObservedAt                 time.Time                   `json:"observed_at"`
	Coverage                   string                      `json:"coverage"`
	Retention                  EventReplayPreviewRetention `json:"retention"`
	ScannedCount               int                         `json:"scanned_count"`
	MatchedCount               int                         `json:"matched_count"`
	FilterMismatchCount        int                         `json:"filter_mismatch_count"`
	PatternMismatchCount       int                         `json:"pattern_mismatch_count"`
	ExpiredCount               int                         `json:"expired_count"`
	AlreadyCapturedCount       int                         `json:"already_captured_count"`
	Matches                    []EventReplayPreviewMatch   `json:"matches"`
	NextAfter                  string                      `json:"next_after,omitempty"`
}

// PreviewEventReplay is a read-only preview for one current ordinary application
// subscription. It neither pins retained events nor creates deliveries.
func (c *Client) PreviewEventReplay(ctx context.Context, appSlug, subscriptionID string, options EventReplayPreviewOptions) (EventReplayPreviewResponse, error) {
	q := url.Values{"from": {options.From.UTC().Format(time.RFC3339Nano)}, "until": {options.Until.UTC().Format(time.RFC3339Nano)}}
	if options.After != "" {
		q.Set("after", options.After)
	}
	if options.Limit != 0 {
		q.Set("limit", strconv.Itoa(options.Limit))
	}
	var out EventReplayPreviewResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(appSlug)+"/event-subscriptions/"+url.PathEscape(subscriptionID)+"/replay-preview?"+q.Encode(), nil, &out)
	return out, err
}
