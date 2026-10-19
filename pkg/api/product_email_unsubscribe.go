package api

import "time"

// The bearer capability response deliberately contains no account, workspace,
// actor, address or delivery identifiers. Current is checked separately from the
// historical acknowledgement so replay cannot imply that later consent changed.
type ProductEmailUnsubscribeResult struct {
	ReceiptID      string    `json:"receipt_id"`
	UnsubscribedAt time.Time `json:"unsubscribed_at"`
	Current        bool      `json:"current"`
}

// Opt-out history is available only through authenticated, scoped routes.
type ProductEmailOptOutReceipt struct {
	ID              string    `json:"id"`
	RecipientID     string    `json:"recipient_id"`
	TenantID        string    `json:"tenant_id"`
	DeliveryID      string    `json:"delivery_id"`
	ExpectedVersion int64     `json:"expected_version"`
	Version         int64     `json:"version"`
	Generation      int64     `json:"generation"`
	UnsubscribedAt  time.Time `json:"unsubscribed_at"`
}
type ProductEmailOptOutPage struct {
	AccountID   string                      `json:"account_id"`
	AppID       string                      `json:"app_id"`
	TenantID    string                      `json:"tenant_id,omitempty"`
	RecipientID string                      `json:"recipient_id,omitempty"`
	ObservedAt  time.Time                   `json:"observed_at"`
	WindowStart time.Time                   `json:"window_start"`
	Receipts    []ProductEmailOptOutReceipt `json:"receipts"`
	NextCursor  string                      `json:"next_cursor,omitempty"`
}
