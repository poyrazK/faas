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

func (c *PreviewClient) GetEventRecipientBlocker(ctx context.Context, query EventRecipientBlockerQuery) (EventRecipientBlockerResponse, error) {
	var out EventRecipientBlockerResponse
	if err := query.Validate(); err != nil {
		return out, err
	}
	values := url.Values{"source": {query.Source}, "id": {query.ID}, "subscription_id": {query.SubscriptionID}, "expected_accepted_at": {query.ExpectedAcceptedAt.Format(time.RFC3339Nano)}}
	err := c.do(ctx, http.MethodGet, "/v1/events/recipient-blocker?"+values.Encode(), nil, &out)
	return out, err
}

// EventRecipientBlockerBatchRequest preserves ordered, independent acceptance guards.
type EventRecipientBlockerBatchRequest struct {
	Queries []EventRecipientBlockerQuery `json:"queries"`
}

func (r EventRecipientBlockerBatchRequest) Validate() error {
	if len(r.Queries) == 0 || len(r.Queries) > EventRecipientBlockerBatchMax {
		return fmt.Errorf("queries must contain 1..%d recipients", EventRecipientBlockerBatchMax)
	}
	for i, q := range r.Queries {
		if err := q.Validate(); err != nil {
			return fmt.Errorf("query %d: %w", i, err)
		}
	}
	return nil
}

// Each result echoes its query; errors never contain replacement evidence.
type EventRecipientBlockerBatchItem struct {
	Query  EventRecipientBlockerQuery     `json:"query"`
	Result *EventRecipientBlockerResponse `json:"result,omitempty"`
	Error  string                         `json:"error,omitempty"`
}
type EventRecipientBlockerBatchResponse struct {
	Items []EventRecipientBlockerBatchItem `json:"items"`
}

func (c *PreviewClient) GetEventRecipientBlockers(ctx context.Context, request EventRecipientBlockerBatchRequest) (EventRecipientBlockerBatchResponse, error) {
	var out EventRecipientBlockerBatchResponse
	if err := request.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/events/recipient-blockers", request, &out)
	return out, err
}

const EventRecipientBlockerIdentityMaxBytes = 256
const EventRecipientBlockerBatchMax = 50

// Mirrors the existing request byte bound in pkg/api/limits.go.
const EventRecipientBlockerBatchBodyMaxBytes = 65536
