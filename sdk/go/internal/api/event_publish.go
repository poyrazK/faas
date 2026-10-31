package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// PublishEventRequest is one account-level event envelope. Source and id
// identify the event: retry an uncertain publish with the original source, id
// and content, never a new id.
type PublishEventRequest struct {
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            *time.Time      `json:"time,omitempty"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	Data            json.RawMessage `json:"data"`
	AccountID       string          `json:"accountid,omitempty"`
	SchemaVersion   string          `json:"schemaversion,omitempty"`
}

// PublishEventResponse is the durable acceptance receipt. Acceptance is not
// delivery; follow ReceiptURL for routing and execution outcomes.
type PublishEventResponse struct {
	ReceiptURL    string    `json:"receipt_url"`
	ID            string    `json:"id"`
	ClientEventID string    `json:"client_event_id,omitempty"`
	AcceptedAt    time.Time `json:"accepted_at"`
	AccountID     string    `json:"account_id,omitempty"`
}

// PublishEventBatchRequest accepts 1–100 independent envelopes in input order.
type PublishEventBatchRequest struct {
	Events []PublishEventRequest `json:"events"`
}

// PublishEventBatchResult uses a zero-based input index. Status is accepted,
// duplicate, rejected or unknown; retry unknown and retryable rejections with
// the original identity.
type PublishEventBatchResult struct {
	Index     int                   `json:"index"`
	Status    string                `json:"status"`
	Retryable bool                  `json:"retryable"`
	Receipt   *PublishEventResponse `json:"receipt,omitempty"`
	Problem   *Problem              `json:"problem,omitempty"`
}

// PublishEventBatchResponse has one result per input; HTTP 200 can include
// rejected or unknown items.
type PublishEventBatchResponse struct {
	Results []PublishEventBatchResult `json:"results"`
}

// PublishEvent durably accepts one event on the account routes
// (POST /v1/events:publish); matching subscriptions and workflows receive it
// asynchronously.
func (c *PreviewClient) PublishEvent(ctx context.Context, req PublishEventRequest) (PublishEventResponse, error) {
	var out PublishEventResponse
	err := c.do(ctx, http.MethodPost, "/v1/events:publish", req, &out)
	return out, err
}

// PublishEventBatch accepts up to 100 events in one request
// (POST /v1/events:publish-batch). Inspect every result.
func (c *PreviewClient) PublishEventBatch(ctx context.Context, req PublishEventBatchRequest) (PublishEventBatchResponse, error) {
	var out PublishEventBatchResponse
	err := c.do(ctx, http.MethodPost, "/v1/events:publish-batch", req, &out)
	return out, err
}
