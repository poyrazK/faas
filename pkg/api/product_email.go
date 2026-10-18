package api

import "time"

// ProductEmailPreferences belong to the signed-in person in one workspace.
// Email addresses and recipient grants are never part of this API.
type ProductEmailPreferences struct {
	WorkOutcomes          bool       `json:"work_outcomes"`
	HelpReplies           bool       `json:"help_replies"`
	Version               int64      `json:"version"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	Mode                  string     `json:"mode"`
	DeliveryPageSize      int        `json:"delivery_page_size"`
	Available             bool       `json:"available"`
	Generation            int64      `json:"generation"`
	PausedAt              *time.Time `json:"paused_at,omitempty"`
	RecoveryBlockedReason string     `json:"recovery_blocked_reason,omitempty"`
	PauseReason           string     `json:"pause_reason,omitempty"`
}

type ProductEmailUpdate struct {
	WorkOutcomes    *bool  `json:"work_outcomes"`
	HelpReplies     *bool  `json:"help_replies"`
	ExpectedVersion *int64 `json:"expected_version"`
}

type ProductEmailDelivery struct {
	ID             string     `json:"id"`
	NotificationID string     `json:"notification_id"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason,omitempty"`
	Attempts       int        `json:"attempts"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ReportedAt     *time.Time `json:"reported_at,omitempty"`
}
