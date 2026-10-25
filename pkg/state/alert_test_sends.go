package state

import (
	"context"
	"encoding/json"
	"time"
)

// Alert test-send kinds (ADR 20261022111812, section 3).
const (
	AlertTestSendChannel     = "notification_channel"
	AlertTestSendRuleWebhook = "alert_rule_webhook"
)

// Alert test-send states.
const (
	AlertTestSendPending = "pending"
	AlertTestSendClaimed = "claimed"
	AlertTestSendDone    = "done"
)

// AlertTestSend is a test send apid relays to meterd, which holds the key
// that opens the destination. Event carries what to send for a rule
// webhook; a channel test needs only its subject.
type AlertTestSend struct {
	ID         string
	AccountID  string
	Kind       string
	SubjectID  string
	Event      json.RawMessage
	State      string
	ClaimToken string
	Result     *AlertTestSendResult
	CreatedAt  time.Time
}

// AlertTestSendResult is what meterd reports for one test send.
type AlertTestSendResult struct {
	Delivered  bool `json:"delivered"`
	StatusCode int  `json:"status_code,omitempty"`
	Attempts   int  `json:"attempts,omitempty"`
	// Failure names why nothing was delivered when meterd could not try:
	// not_found or unopenable. Error carries the send error otherwise.
	Failure string `json:"failure,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Alert test-send failures meterd reports without trying a delivery.
const (
	AlertTestSendNotFound   = "not_found"
	AlertTestSendUnopenable = "unopenable"
)

// AlertTestSendStore keeps relayed test sends. It is not part of Store: apid
// and meterd reach it through a type assertion.
type AlertTestSendStore interface {
	// CreateAlertTestSend records a pending request.
	CreateAlertTestSend(ctx context.Context, in AlertTestSend) (AlertTestSend, error)
	// GetAlertTestSend returns the account's request, or ErrNotFound.
	GetAlertTestSend(ctx context.Context, accountID, id string) (AlertTestSend, error)
	// ClaimAlertTestSend claims the oldest pending request, or one whose
	// claim lapsed; ok is false when there is none.
	ClaimAlertTestSend(ctx context.Context, lease time.Duration) (AlertTestSend, bool, error)
	// CompleteAlertTestSend stores the result while the claim still holds.
	CompleteAlertTestSend(ctx context.Context, id, claimToken string, result AlertTestSendResult) error
	// DeleteAlertTestSend removes the account's request.
	DeleteAlertTestSend(ctx context.Context, accountID, id string) error
	// PruneAlertTestSends removes requests created before cutoff.
	PruneAlertTestSends(ctx context.Context, cutoff time.Time) (int, error)
}

func validAlertTestSend(in AlertTestSend) bool {
	return (in.Kind == AlertTestSendChannel || in.Kind == AlertTestSendRuleWebhook) &&
		in.ID != "" && in.AccountID != "" && in.SubjectID != "" && len(in.Event) <= 65536
}
