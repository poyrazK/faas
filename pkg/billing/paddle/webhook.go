package paddle

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/billing"
)

// parsePaddleEvent decodes a Paddle webhook body into the
// normalized billing.Event. Provider-shaped JSON stays here —
// apid sees only the Event envelope.
//
// Shape (verified against paddle-go-sdk/v5@v5.2.0 event_types.go):
//
//	{
//	  "event_id":   "evt_01HV...",
//	  "event_type": "subscription.created",
//	  "data": { ... provider-specific payload ... }
//	}
//
// Customer / subscription / plan ids are best-effort pulled from
// the inner data block; apid resolves customer → account via the
// state store in PR #3.
func parsePaddleEvent(payload []byte) (billing.Event, error) {
	var raw struct {
		EventID   string          `json:"event_id"`
		EventType string          `json:"event_type"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return billing.Event{}, fmt.Errorf("paddle: parse webhook body: %w", err)
	}

	custID, subID, planID := extractIDs(raw.Data)

	return billing.Event{
		Type:           mapPaddleEventType(raw.EventType),
		CustomerID:     custID,
		SubscriptionID: subID,
		PlanID:         planID,
		Raw:            cloneBytes(payload),
	}, nil
}

// extractIDs pulls customer / subscription / plan ids off the
// provider-specific data block. Paddle nests these under
// data.{customer_id,subscription_id,items[0].price.id} — best-
// effort because every event type carries a different shape, and
// the apid PR #3 dispatcher can re-fetch the latest values from
// Paddle if any of these come back empty.
func extractIDs(data json.RawMessage) (customer, subscription, plan string) {
	if len(data) == 0 {
		return
	}
	// Subscription events: data has { customer_id, subscription_id, items: [...] }.
	var sub struct {
		CustomerID     string `json:"customer_id"`
		SubscriptionID string `json:"subscription_id"`
		Items          []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &sub); err == nil {
		customer = sub.CustomerID
		subscription = sub.SubscriptionID
		if len(sub.Items) > 0 {
			plan = sub.Items[0].Price.ID
		}
		return
	}

	// Transaction events: data has { customer_id, subscription_id? }.
	var txn struct {
		CustomerID     string `json:"customer_id"`
		SubscriptionID string `json:"subscription_id"`
	}
	if err := json.Unmarshal(data, &txn); err == nil {
		customer = txn.CustomerID
		subscription = txn.SubscriptionID
	}
	return
}

// mapPaddleEventType translates Paddle's event_type strings into
// the normalized billing.EventType. Unknown types return
// EventUnknown so apid's switch falls through to a 200 no-op
// (Paddle retries on 5xx; we 200 unknown types so it doesn't retry
// forever — same contract as pkg/billing/stripe's mapStripeEventType).
//
// Note on naming: Paddle's "subscription.canceled" is the past
// tense of cancel — same word apid already normalizes to
// EventSubscriptionCanceled for Stripe's `customer.subscription.deleted`.
func mapPaddleEventType(t string) billing.EventType {
	switch t {
	case "subscription.created":
		return billing.EventSubscriptionCreated
	case "subscription.updated":
		return billing.EventSubscriptionUpdated
	case "subscription.canceled":
		return billing.EventSubscriptionCanceled
	case "subscription.past_due":
		return billing.EventSubscriptionPastDue
	case "transaction.paid":
		return billing.EventPaymentSucceeded
	case "transaction.payment_failed":
		return billing.EventPaymentFailed
	default:
		return billing.EventUnknown
	}
}
