package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

type CommitSourceResponse struct {
	ID              string     `json:"id"`
	AppID           string     `json:"app_id"`
	Name            string     `json:"name"`
	Enabled         bool       `json:"enabled"`
	OperationPolicy string     `json:"operation_policy,omitempty"`
	RelayStatus     string     `json:"relay_status,omitempty"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	PendingEvents   *int64     `json:"pending_events,omitempty"`
	BlockedEvents   *int64     `json:"blocked_events,omitempty"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
}

func (c *Client) GetCommitSource(ctx context.Context, source string) (CommitSourceResponse, error) {
	var out CommitSourceResponse
	err := c.do(ctx, "GET", "/v1/commit-sources/"+url.PathEscape(source), nil, &out)
	return out, err
}

type CommitReceiptResponse struct {
	ID           string    `json:"receipt_id"`
	SourceID     string    `json:"source_id"`
	EventID      string    `json:"event_id"`
	InvocationID string    `json:"invocation_id,omitempty"`
	OperationID  string    `json:"operation_id,omitempty"`
	AcceptedAt   time.Time `json:"accepted_at"`
	OperationURL string    `json:"operation_url"`
}

type CommitOperationResponse struct {
	ID          string     `json:"id"`
	ReceiptID   string     `json:"receipt_id"`
	SourceID    string     `json:"source_id"`
	EventID     string     `json:"event_id"`
	State       string     `json:"state"`
	AcceptedAt  time.Time  `json:"accepted_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func (c *Client) GetCommitOperation(ctx context.Context, id string) (CommitOperationResponse, error) {
	var out CommitOperationResponse
	err := c.do(ctx, "GET", "/v1/operations/"+url.PathEscape(id), nil, &out)
	return out, err
}

type CommitEventRequest struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type CommitBlockedEventResponse struct {
	EventID    string    `json:"event_id"`
	Type       string    `json:"type"`
	Code       string    `json:"blocked_code"`
	CreatedAt  time.Time `json:"created_at"`
	ObservedAt time.Time `json:"observed_at"`
}
type CommitBlockedEventsResponse struct {
	Items       []CommitBlockedEventResponse `json:"items"`
	Limit       int                          `json:"limit"`
	Observation string                       `json:"observation"`
}

func (c *Client) ListCommitBlockedEvents(ctx context.Context, source string) (CommitBlockedEventsResponse, error) {
	var out CommitBlockedEventsResponse
	err := c.do(ctx, "GET", "/v1/commit-sources/"+url.PathEscape(source)+"/blocked-events", nil, &out)
	return out, err
}
func (c *Client) ReplayCommitBlockedEvent(ctx context.Context, source, event string) error {
	return c.do(ctx, "POST", "/v1/commit-sources/"+url.PathEscape(source)+"/events/"+url.PathEscape(event)+"/replay", nil, nil)
}

func (c *Client) CreateCommitSource(ctx context.Context, slug, name, operationPolicy string) (CommitSourceResponse, error) {
	var out CommitSourceResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/commit-sources", map[string]string{"name": name, "operation_policy": operationPolicy}, &out)
	return out, err
}
func (c *Client) SetCommitSourceEnabled(ctx context.Context, source string, enabled bool) (CommitSourceResponse, error) {
	var out CommitSourceResponse
	err := c.do(ctx, "PATCH", "/v1/commit-sources/"+url.PathEscape(source), map[string]bool{"enabled": enabled}, &out)
	return out, err
}
func (c *Client) PutCommitSourceConnection(ctx context.Context, source, connection string) error {
	return c.do(ctx, "PUT", "/v1/commit-sources/"+url.PathEscape(source)+"/connection", map[string]string{"connection_url": connection}, nil)
}
func (c *Client) AcceptCommitEvent(ctx context.Context, source string, event CommitEventRequest) (CommitReceiptResponse, error) {
	var out CommitReceiptResponse
	err := c.do(ctx, "POST", "/v1/commit-sources/"+url.PathEscape(source)+"/events", event, &out)
	return out, err
}
func (c *Client) GetCommitReceipt(ctx context.Context, source, event string) (CommitReceiptResponse, error) {
	var out CommitReceiptResponse
	err := c.do(ctx, "GET", "/v1/commit-sources/"+url.PathEscape(source)+"/events/"+url.PathEscape(event), nil, &out)
	return out, err
}
