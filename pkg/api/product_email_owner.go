package api

import "time"

// ProductEmailOwnerPage is a retained queue observation. Counts cover the whole
// selected customer/product window, independently of the history status filter.
// It contains no recipient addresses, Auth identities, grants or message bodies.
type ProductEmailOwnerPage struct {
	AccountID       string                      `json:"account_id"`
	AppID           string                      `json:"app_id"`
	TenantID        string                      `json:"tenant_id,omitempty"`
	Status          string                      `json:"status"`
	Mode            string                      `json:"mode"`
	ObservedAt      time.Time                   `json:"observed_at"`
	WindowStart     time.Time                   `json:"window_start"`
	Counts          ProductEmailCounts          `json:"counts"`
	OldestPendingAt *time.Time                  `json:"oldest_pending_at,omitempty"`
	Deliveries      []ProductEmailOwnerDelivery `json:"deliveries"`
	NextCursor      string                      `json:"next_cursor,omitempty"`
}

type ProductEmailCounts struct {
	Queued           int64 `json:"queued"`
	Leased           int64 `json:"leased"`
	Captured         int64 `json:"captured"`
	ProviderAccepted int64 `json:"provider_accepted"`
	Failed           int64 `json:"failed"`
	Cancelled        int64 `json:"cancelled"`
	Delivered        int64 `json:"delivered"`
	Bounced          int64 `json:"bounced"`
	Complained       int64 `json:"complained"`
}

type ProductEmailOwnerDelivery struct {
	ProductEmailDelivery
	TenantID           string     `json:"tenant_id"`
	RecipientID        string     `json:"recipient_id"`
	NextAttemptAt      time.Time  `json:"next_attempt_at"`
	Deadline           time.Time  `json:"deadline"`
	RetryEligible      bool       `json:"retry_eligible"`
	RetryBlockedReason string     `json:"retry_blocked_reason,omitempty"`
	PausedAt           *time.Time `json:"paused_at,omitempty"`
	PauseReason        string     `json:"pause_reason,omitempty"`
}

type ProductEmailRetryRequest struct {
	TenantID         string `json:"tenant_id"`
	ExpectedAttempts *int   `json:"expected_attempts"`
}

// A receipt acknowledges one requeue, not a send. Its delivery and failed
// attempt identify a durable idempotent action, even after later attempts finish.
type ProductEmailRetryReceipt struct {
	DeliveryID    string    `json:"delivery_id"`
	TenantID      string    `json:"tenant_id"`
	FailedAttempt int       `json:"failed_attempt"`
	QueuedAt      time.Time `json:"queued_at"`
}
