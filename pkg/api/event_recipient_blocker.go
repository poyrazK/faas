package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EventRecipientBlockerQuery pins one retained acceptance and recipient.
type EventRecipientBlockerQuery struct {
	Source             string    `json:"source"`
	ID                 string    `json:"id"`
	SubscriptionID     string    `json:"subscription_id"`
	ExpectedAcceptedAt time.Time `json:"expected_accepted_at"`
}

func (q EventRecipientBlockerQuery) Validate() error {
	for _, value := range []string{q.Source, q.ID, q.SubscriptionID} {
		if value == "" || len(value) > EventRecipientBlockerIdentityMaxBytes {
			return fmt.Errorf("source, id and subscription_id are required and must be at most %d bytes", EventRecipientBlockerIdentityMaxBytes)
		}
	}
	if q.ExpectedAcceptedAt.IsZero() {
		return fmt.Errorf("expected_accepted_at is required")
	}
	return (AppEventAcceptanceGuard{ExpectedAcceptedAt: &q.ExpectedAcceptedAt}).Validate()
}

// EventRecipientBlockerResponse describes routing only, never handler completion.
type EventRecipientBlockerResponse struct {
	Query        EventRecipientBlockerQuery `json:"query"`
	ObservedAt   time.Time                  `json:"observed_at"`
	Status       string                     `json:"status"`
	Reason       string                     `json:"reason,omitempty"`
	RoutingState string                     `json:"routing_state,omitempty"`
	Recipient    *EventBacklogRecipient     `json:"recipient,omitempty"`
}

func (c *Client) GetEventRecipientBlocker(ctx context.Context, query EventRecipientBlockerQuery) (EventRecipientBlockerResponse, error) {
	var out EventRecipientBlockerResponse
	if err := query.Validate(); err != nil {
		return out, err
	}
	values := url.Values{"source": {query.Source}, "id": {query.ID}, "subscription_id": {query.SubscriptionID}, "expected_accepted_at": {query.ExpectedAcceptedAt.Format(time.RFC3339Nano)}}
	err := c.do(ctx, http.MethodGet, "/v1/events/recipient-blocker?"+values.Encode(), nil, &out)
	return out, err
}
