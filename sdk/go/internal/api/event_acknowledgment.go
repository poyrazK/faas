package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Zero retains ordinary HTTP-success acknowledgment for new acceptances.
type EventAcknowledgmentPolicy struct {
	SubscriptionID string `json:"subscription_id"`
	LeaseSeconds   int    `json:"lease_seconds"`
}
type EventAcknowledgmentPolicyRequest struct {
	LeaseSeconds *int `json:"lease_seconds"`
}

// The token is an additional fence, never a substitute for app write authority.
type EventAcknowledgmentRequest struct {
	Token  string          `json:"token"`
	Result json.RawMessage `json:"result,omitempty"`
}
type EventAcknowledgmentFailureRequest struct {
	Token  string `json:"token"`
	Reason string `json:"reason"`
}
type EventAcknowledgmentStatus struct {
	Disposition      string     `json:"disposition,omitempty"`
	FailureReason    string     `json:"failure_reason,omitempty"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	InvocationID     string     `json:"invocation_id"`
	Attempt          int        `json:"attempt"`
	ReplayGeneration int64      `json:"replay_generation"`
	State            string     `json:"state"`
	ProcessingState  string     `json:"processing_state"`
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	DeadlineAt       *time.Time `json:"deadline_at,omitempty"`
}

func acknowledgmentPolicyPath(app, sub string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(sub) + "/acknowledgment-policy"
}
func acknowledgmentPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-deliveries/" + url.PathEscape(id) + "/acknowledgment"
}
func (c *Client) GetEventAcknowledgmentPolicy(ctx context.Context, app, sub string) (EventAcknowledgmentPolicy, error) {
	var out EventAcknowledgmentPolicy
	err := c.do(ctx, http.MethodGet, acknowledgmentPolicyPath(app, sub), nil, &out)
	return out, err
}
func (c *Client) SetEventAcknowledgmentPolicy(ctx context.Context, app, sub string, req EventAcknowledgmentPolicyRequest) (EventAcknowledgmentPolicy, error) {
	var out EventAcknowledgmentPolicy
	err := c.do(ctx, http.MethodPut, acknowledgmentPolicyPath(app, sub), req, &out)
	return out, err
}
func (c *Client) GetEventAcknowledgment(ctx context.Context, app, id string) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodGet, acknowledgmentPath(app, id), nil, &out)
	return out, err
}
func (c *Client) RenewEventAcknowledgment(ctx context.Context, app, id string, req EventAcknowledgmentRequest) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodPost, acknowledgmentPath(app, id)+"/renew", req, &out)
	return out, err
}
func (c *Client) CompleteEventAcknowledgment(ctx context.Context, app, id string, req EventAcknowledgmentRequest) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodPost, acknowledgmentPath(app, id)+"/complete", req, &out)
	return out, err
}

func (c *Client) RetryEventAcknowledgment(ctx context.Context, app, id string, req EventAcknowledgmentFailureRequest) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodPost, acknowledgmentPath(app, id)+"/retry", req, &out)
	return out, err
}
func (c *Client) DeadLetterEventAcknowledgment(ctx context.Context, app, id string, req EventAcknowledgmentFailureRequest) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodPost, acknowledgmentPath(app, id)+"/dead-letter", req, &out)
	return out, err
}

// Processing counts cover retained opted-in invocations, not pruned history.
type EventConsumerProcessingHealth struct {
	RenewalWindow                   *EventAcknowledgmentWindowHealth `json:"renewal_window,omitempty"`
	AppID                           string                           `json:"app_id"`
	SubscriptionID                  string                           `json:"subscription_id"`
	ObservedAt                      time.Time                        `json:"observed_at"`
	WindowStart                     time.Time                        `json:"window_start"`
	Coverage                        string                           `json:"coverage"`
	Complete                        bool                             `json:"complete"`
	Waiting                         int64                            `json:"waiting"`
	Delivering                      int64                            `json:"delivering"`
	Processing                      int64                            `json:"processing"`
	Expired                         int64                            `json:"expired"`
	Completed                       int64                            `json:"completed"`
	DeadLettered                    int64                            `json:"dead_lettered"`
	Failed                          int64                            `json:"failed"`
	Other                           int64                            `json:"other"`
	NearExpiry                      int64                            `json:"near_expiry"`
	NearExpirySeconds               int                              `json:"near_expiry_seconds"`
	OldestProcessingOfferedAt       *time.Time                       `json:"oldest_processing_offered_at,omitempty"`
	OldestProcessingAgeSeconds      float64                          `json:"oldest_processing_age_seconds"`
	AcknowledgmentSamples           int64                            `json:"acknowledgment_samples"`
	AcknowledgmentLatencyMaxSeconds float64                          `json:"acknowledgment_latency_max_seconds"`
	RenewalFailureCoverage          string                           `json:"renewal_failure_coverage"`
}

// Repeated operations share a row; first/last timestamps bound the occurrences.
type EventAcknowledgmentOutcome struct {
	InvocationID     string    `json:"invocation_id"`
	Attempt          int       `json:"attempt"`
	ReplayGeneration int64     `json:"replay_generation"`
	Kind             string    `json:"kind"`
	FirstAt          time.Time `json:"first_at"`
	LastAt           time.Time `json:"last_at"`
	Occurrences      int64     `json:"occurrences"`
	RetainUntil      time.Time `json:"retain_until"`
}
type EventAcknowledgmentHistory struct {
	Outcomes  []EventAcknowledgmentOutcome `json:"outcomes"`
	Truncated bool                         `json:"truncated"`
	Coverage  string                       `json:"coverage"`
}

// An empty deliveryID selects the consumer's most recent outcome groups.
func (c *Client) ListEventAcknowledgmentHistory(ctx context.Context, app, sub, deliveryID string) (EventAcknowledgmentHistory, error) {
	var out EventAcknowledgmentHistory
	path := "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(sub) + "/acknowledgment-history"
	if deliveryID != "" {
		path += "?invocation_id=" + url.QueryEscape(deliveryID)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Time filters select groups by immutable first occurrence, not individual operations.
type EventAcknowledgmentHistoryQuery struct {
	InvocationID string     `json:"invocation_id,omitempty"`
	Kind         string     `json:"kind,omitempty"`
	From         *time.Time `json:"from,omitempty"`
	Until        *time.Time `json:"until,omitempty"`
	Limit        int        `json:"limit,omitempty"`
	Cursor       string     `json:"cursor,omitempty"`
}
type EventAcknowledgmentHistoryPage struct {
	Outcomes   []EventAcknowledgmentOutcome `json:"outcomes"`
	HasMore    bool                         `json:"has_more"`
	NextCursor string                       `json:"next_cursor,omitempty"`
	Cutoff     time.Time                    `json:"cutoff"`
	Coverage   string                       `json:"coverage"`
}

func (c *Client) ListEventAcknowledgmentHistoryPage(ctx context.Context, app, sub string, query EventAcknowledgmentHistoryQuery) (EventAcknowledgmentHistoryPage, error) {
	var out EventAcknowledgmentHistoryPage
	values := url.Values{}
	if query.InvocationID != "" {
		values.Set("invocation_id", query.InvocationID)
	}
	if query.Kind != "" {
		values.Set("kind", query.Kind)
	}
	if query.From != nil {
		values.Set("from", query.From.Format(time.RFC3339Nano))
	}
	if query.Until != nil {
		values.Set("until", query.Until.Format(time.RFC3339Nano))
	}
	if query.Limit != 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	path := "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(sub) + "/acknowledgment-history/pages"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Full-minute counts cover recorded operations since upgraded writers began recording.
// Fractions describe recorded renewal operations, not unique deliveries.
type EventAcknowledgmentWindowHealth struct {
	RecordingCoverage        *EventAcknowledgmentRecordingCoverage `json:"recording_coverage,omitempty"`
	WindowStart              time.Time                             `json:"window_start"`
	WindowEnd                time.Time                             `json:"window_end"`
	BucketSeconds            int                                   `json:"bucket_seconds"`
	RetentionSeconds         int                                   `json:"retention_seconds"`
	Coverage                 string                                `json:"coverage"`
	RenewalSuccesses         int64                                 `json:"renewal_successes"`
	RenewalRejections        int64                                 `json:"renewal_rejections"`
	RenewalAttempts          int64                                 `json:"renewal_attempts"`
	LeaseExpiries            int64                                 `json:"lease_expiries"`
	Saturated                bool                                  `json:"saturated"`
	RenewalRejectionFraction *float64                              `json:"renewal_rejection_fraction,omitempty"`
	LeaseExpiryRatePerSecond *float64                              `json:"lease_expiry_rate_per_second,omitempty"`
}

// Verification covers the exhaustive deployment-declared writer inventory only.
type EventAcknowledgmentRecordingCoverage struct {
	Verified              bool       `json:"verified"`
	Reason                string     `json:"reason"`
	ObservedAt            time.Time  `json:"observed_at"`
	ContinuousSince       *time.Time `json:"continuous_since,omitempty"`
	VerifiedThrough       *time.Time `json:"verified_through,omitempty"`
	InventoryRevision     string     `json:"inventory_revision,omitempty"`
	ExpectedWriters       int        `json:"expected_writers"`
	ActiveWriters         int        `json:"active_writers"`
	CapabilityVersion     int        `json:"capability_version"`
	HeartbeatLeaseSeconds int        `json:"heartbeat_lease_seconds"`
}

// A verified heartbeat watermark delays the window end until all writers have observed it.
func EventAcknowledgmentWindowBounds(observed time.Time, window time.Duration, coverage *EventAcknowledgmentRecordingCoverage) (time.Time, time.Time) {
	end := observed.UTC().Truncate(EventAcknowledgmentMetricBucket)
	requested := observed.Add(-window).UTC()
	start := requested.Truncate(EventAcknowledgmentMetricBucket)
	if start.Before(requested) {
		start = start.Add(EventAcknowledgmentMetricBucket)
	}
	if coverage != nil && coverage.Verified && coverage.VerifiedThrough != nil {
		through := coverage.VerifiedThrough.UTC().Truncate(EventAcknowledgmentMetricBucket)
		if through.Before(end) {
			end = through
		}
	}
	if start.After(end) {
		end = start
	}
	return start, end
}

const EventProcessingNearExpiry = 30 * time.Second
const EventProcessingAlertSustain = 30 * time.Second
