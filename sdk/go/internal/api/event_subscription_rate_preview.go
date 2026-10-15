package api

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// EventSubscriptionRatePreviewRequest has no command UUID and performs no writes.
type EventSubscriptionRatePreviewRequest struct {
	Enabled        *bool `json:"enabled"`
	IntervalMillis int64 `json:"interval_millis"`
	Burst          int64 `json:"burst"`
}

func (r EventSubscriptionRatePreviewRequest) Validate() error {
	if r.Enabled == nil || r.IntervalMillis < EventSubscriptionRateIntervalMinMillis || r.IntervalMillis > EventSubscriptionRateIntervalMaxMillis || r.Burst < 1 || r.Burst > EventSubscriptionRateBurstMax {
		return fmt.Errorf("preview requires explicit enabled, interval 10..3600000 ms and burst 1..100")
	}
	return nil
}

type EventSubscriptionRatePreview struct {
	DeadlineCoverage       string                              `json:"deadline_coverage,omitempty"`
	DeadlineRecipients     *int                                `json:"deadline_recipients,omitempty"`
	EarliestDeadlineAt     *time.Time                          `json:"earliest_deadline_at,omitempty"`
	Coverage               string                              `json:"coverage"`
	AppID                  string                              `json:"app_id"`
	SubscriptionID         string                              `json:"subscription_id"`
	ObservedAt             time.Time                           `json:"observed_at"`
	Advisory               bool                                `json:"advisory"`
	ReadOnly               bool                                `json:"read_only"`
	ExpectedRevision       int64                               `json:"expected_revision"`
	Current                EventSubscriptionRateLimit          `json:"current"`
	Projected              EventSubscriptionRateLimit          `json:"projected"`
	Paused                 bool                                `json:"paused"`
	LegacyRatePerSecond    int                                 `json:"legacy_rate_per_second"`
	CircuitState           string                              `json:"circuit_state"`
	CapacityBlockingScopes []string                            `json:"capacity_blocking_scopes"`
	Constraints            []string                            `json:"constraints"`
	SampleLimit            int                                 `json:"sample_limit"`
	SampledPending         int                                 `json:"sampled_pending"`
	Complete               bool                                `json:"complete"`
	Truncated              bool                                `json:"truncated"`
	RateOnlyDrainMillis    *int64                              `json:"rate_only_drain_millis,omitempty"`
	DeadlineRiskCount      int                                 `json:"deadline_risk_count"`
	AlreadyExpiredCount    int                                 `json:"already_expired_count"`
	DeadlineRisks          []EventSubscriptionRateDeadlineRisk `json:"deadline_risks"`
	RisksTruncated         bool                                `json:"risks_truncated"`
}
type EventSubscriptionRateDeadlineRisk struct {
	EventSource        string     `json:"event_source"`
	EventID            string     `json:"event_id"`
	AcceptedAt         time.Time  `json:"accepted_at"`
	DeadlineAt         time.Time  `json:"deadline_at"`
	ModeledAdmissionAt *time.Time `json:"modeled_admission_at,omitempty"`
	Reason             string     `json:"reason"`
}

func (c *Client) PreviewEventSubscriptionRateLimit(ctx context.Context, app, sub string, req EventSubscriptionRatePreviewRequest) (EventSubscriptionRatePreview, error) {
	var out EventSubscriptionRatePreview
	err := c.do(ctx, http.MethodPost, subscriptionDeliveryControlPath(app, sub)+"/rate-limit/preview", req, &out)
	return out, err
}
