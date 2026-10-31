package state

import (
	"crypto/md5" // #nosec G501 -- mirrors the capture trigger's md5()::uuid entry identity; not a security boundary.
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// eventRouteSourceID mirrors the capture trigger's
// md5(outbox_id || '/' || subscription_id)::uuid.
func eventRouteSourceID(outboxID int64, subscriptionID string) string {
	return uuid.UUID(md5.Sum(fmt.Appendf(nil, "%d/%s", outboxID, subscriptionID))).String() // #nosec G401 -- identity, not a security boundary.
}

// captureEventRouteDeadLettersLocked mirrors capture_event_route_dead_letters
// (ADR-20261026163443): before a receipt is pruned, each failed recipient
// enters the ledger. An owner that is gone or pending deletion keeps nothing.
func (m *MemStore) captureEventRouteDeadLettersLocked(owner string, work *PublishedEventWork) {
	accountID, ok := m.eventRouteOwnerLocked(owner)
	if !ok {
		return
	}
	var event publishedEventIdentity
	_ = json.Unmarshal(work.Payload, &event)
	for subscriptionID, progress := range work.RecipientProgress {
		if progress.State != PublishedEventRecipientFailed {
			continue
		}
		origin, appID := "event_subscription", ""
		for _, recipient := range work.RecipientSnapshot {
			if recipient.ID != subscriptionID {
				continue
			}
			if len(recipient.Workflow) > 0 {
				origin = "event_workflow"
			}
			for _, app := range m.apps {
				if sameMemUUID(app.ID, recipient.AppID) && sameMemUUID(app.AccountID, accountID) {
					appID = app.ID
				}
			}
			break
		}
		failureCode := progress.FailureCode
		if failureCode == "" {
			failureCode = EventFanoutFailureCodeUnknown
		}
		failedAt := progress.UpdatedAt
		if failedAt.IsZero() {
			failedAt = work.CreatedAt
		}
		detail, _ := json.Marshal(eventRouteDeadLetterDetail{
			SubscriptionID: subscriptionID, EventSource: event.Source, EventID: event.ID, EventType: event.Type,
			AcceptedAt: work.CreatedAt, LastError: progress.LastError, RetryStopReason: progress.RetryStopReason,
			Retryable: progress.Retryable,
		})
		sourceID := eventRouteSourceID(work.ID, subscriptionID)
		id := unifiedDeadLetterEventID(DeadLetterSourceEventRoute, sourceID)
		if _, exists := m.deadLetterSnapshots[id]; exists {
			continue
		}
		if m.deadLetterSnapshots == nil {
			m.deadLetterSnapshots = make(map[string]DeadLetterEvent)
		}
		m.deadLetterSnapshots[id] = DeadLetterEvent{
			ID: id, AccountID: accountID, AppID: appID, Source: DeadLetterSourceEventRoute, SourceID: sourceID,
			Origin: origin, Payload: append(json.RawMessage(nil), work.Payload...), Headers: json.RawMessage(`{}`),
			ErrorKind: failureCode, ErrorDetail: detail, RetryCount: max(progress.Attempts, 0),
			FirstFailedAt: failedAt, LastFailedAt: failedAt, CreatedAt: time.Now().UTC(),
		}
	}
}

// eventRouteDeadLetterDetail is the error_detail the capture trigger builds.
type eventRouteDeadLetterDetail struct {
	SubscriptionID  string    `json:"subscription_id"`
	EventSource     string    `json:"event_source"`
	EventID         string    `json:"event_id"`
	EventType       string    `json:"event_type"`
	AcceptedAt      time.Time `json:"accepted_at"`
	LastError       string    `json:"last_error,omitempty"`
	RetryStopReason string    `json:"retry_stop_reason,omitempty"`
	Retryable       bool      `json:"retryable"`
}

// eventRouteOwnerLocked resolves a receipt owner to the ledger's account ID.
func (m *MemStore) eventRouteOwnerLocked(owner string) (string, bool) {
	for _, account := range m.accounts {
		if sameMemUUID(account.ID, owner) {
			return account.ID, account.Status != AccountDeletedPending
		}
	}
	for _, org := range m.orgs {
		if sameMemUUID(org.ID, owner) {
			return org.ID, true
		}
	}
	return "", false
}
