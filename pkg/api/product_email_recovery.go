package api

import "time"

// Recovery confirms personal consent for a verified replacement address. No
// address, recipient capability or encrypted address evidence crosses this API.
type ProductEmailRecoveryRequest struct {
	ID                 string `json:"id"`
	ExpectedVersion    int64  `json:"expected_version"`
	ExpectedGeneration int64  `json:"expected_generation"`
	WorkOutcomes       *bool  `json:"work_outcomes"`
	HelpReplies        *bool  `json:"help_replies"`
}
type ProductEmailRecoveryReceipt struct {
	ID                 string    `json:"id"`
	RecipientID        string    `json:"recipient_id"`
	TenantID           string    `json:"tenant_id"`
	ExpectedVersion    int64     `json:"expected_version"`
	PreviousGeneration int64     `json:"previous_generation"`
	Version            int64     `json:"version"`
	Generation         int64     `json:"generation"`
	WorkOutcomes       bool      `json:"work_outcomes"`
	HelpReplies        bool      `json:"help_replies"`
	RecoveredAt        time.Time `json:"recovered_at"`
}
type ProductEmailRecoveryPage struct {
	AccountID   string                        `json:"account_id"`
	AppID       string                        `json:"app_id"`
	TenantID    string                        `json:"tenant_id,omitempty"`
	RecipientID string                        `json:"recipient_id,omitempty"`
	ObservedAt  time.Time                     `json:"observed_at"`
	WindowStart time.Time                     `json:"window_start"`
	Receipts    []ProductEmailRecoveryReceipt `json:"receipts"`
	NextCursor  string                        `json:"next_cursor,omitempty"`
}
