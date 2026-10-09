package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type EventRecoveryPreflightItem struct {
	ReceiptRetainUntil   *time.Time `json:"receipt_retain_until,omitempty"`
	ReceiptRetentionHeld bool       `json:"receipt_retention_held,omitempty"`
	Position             int64      `json:"position"`
	Status               string     `json:"status"`
	Reason               string     `json:"reason"`
	CapacityScope        string     `json:"capacity_scope,omitempty"`
}
type EventRecoveryPreflight struct {
	ReceiptProtectionUntil              *time.Time                   `json:"receipt_protection_until,omitempty"`
	ReceiptRetentionWarningCount        int64                        `json:"receipt_retention_warning_count,omitempty"`
	ReceiptRetentionHeldCount           int64                        `json:"receipt_retention_held_count,omitempty"`
	EarliestUnheldRetainUntil           *time.Time                   `json:"earliest_unheld_retain_until,omitempty"`
	MinimumDrainCrossesReceiptRetention bool                         `json:"minimum_drain_crosses_receipt_retention,omitempty"`
	JobID                               string                       `json:"job_id"`
	ObservedAt                          time.Time                    `json:"observed_at"`
	State                               string                       `json:"state"`
	Active                              bool                         `json:"active"`
	PendingCount                        int64                        `json:"pending_count"`
	EligibleCount                       int64                        `json:"eligible_count"`
	WaitingCount                        int64                        `json:"waiting_count"`
	LikelySkippedCount                  int64                        `json:"likely_skipped_count"`
	UnknownCount                        int64                        `json:"unknown_count"`
	ReasonCounts                        map[string]int64             `json:"reason_counts"`
	CapacityScopes                      map[string]int64             `json:"capacity_scopes"`
	RatePerSecond                       int                          `json:"rate_per_second"`
	RemainingLifetimeSeconds            float64                      `json:"remaining_lifetime_seconds"`
	MinimumDrainSeconds                 float64                      `json:"minimum_drain_seconds"`
	EarliestDrainAt                     time.Time                    `json:"earliest_drain_at"`
	FitsBeforeExpiry                    bool                         `json:"fits_before_expiry"`
	AssumesImmediateResume              bool                         `json:"assumes_immediate_resume"`
	Sample                              []EventRecoveryPreflightItem `json:"sample"`
}

func (c *Client) GetEventRecoveryPreflight(ctx context.Context, id string) (EventRecoveryPreflight, error) {
	var out EventRecoveryPreflight
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id)+"/preflight", nil, &out)
	return out, err
}
