package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

type PutWebhookWorkflowBindingRequest struct {
	ExpectedVersion  *int64          `json:"expected_version"`
	WorkflowName     string          `json:"workflow_name"`
	EventType        string          `json:"event_type"`
	Filter           json.RawMessage `json:"filter,omitempty"`
	TakeOverDelivery bool            `json:"take_over_delivery"`
}
type WebhookWorkflowBindingResponse struct {
	EndpointID   string          `json:"endpoint_id"`
	WorkflowName string          `json:"workflow_name"`
	EventType    string          `json:"event_type"`
	Filter       json.RawMessage `json:"filter"`
	Version      int64           `json:"version"`
	UpdatedAt    string          `json:"updated_at"`
}
type WebhookWorkflowReceiptResponse struct {
	ReceiptID       string `json:"receipt_id"`
	EndpointID      string `json:"endpoint_id"`
	ProviderEventID string `json:"provider_event_id"`
	WorkflowName    string `json:"workflow_name"`
	Status          string `json:"status"`
	IgnoredReason   string `json:"ignored_reason,omitempty"`
	Duplicate       bool   `json:"duplicate"`
	AcceptedAt      string `json:"accepted_at"`
	EventSource     string `json:"event_source"`
	RoutingStatus   string `json:"routing_status"`
	RunID           string `json:"run_id,omitempty"`
}

func webhookWorkflowPath(slug, id string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/inbound-webhooks/" + url.PathEscape(id)
}
func (c *Client) PutWebhookWorkflowBinding(ctx context.Context, slug, id string, body PutWebhookWorkflowBindingRequest) (WebhookWorkflowBindingResponse, error) {
	var out WebhookWorkflowBindingResponse
	err := c.do(ctx, "PUT", webhookWorkflowPath(slug, id)+"/workflow-binding", body, &out)
	return out, err
}
func (c *Client) GetWebhookWorkflowBinding(ctx context.Context, slug, id string) (WebhookWorkflowBindingResponse, error) {
	var out WebhookWorkflowBindingResponse
	err := c.do(ctx, "GET", webhookWorkflowPath(slug, id)+"/workflow-binding", nil, &out)
	return out, err
}
func (c *Client) DeleteWebhookWorkflowBinding(ctx context.Context, slug, id string, version int64) error {
	return c.do(ctx, "DELETE", webhookWorkflowPath(slug, id)+"/workflow-binding?expected_version="+strconv.FormatInt(version, 10), nil, nil)
}
func (c *Client) GetWebhookWorkflowReceipt(ctx context.Context, slug, id, eventID string) (WebhookWorkflowReceiptResponse, error) {
	var out WebhookWorkflowReceiptResponse
	err := c.do(ctx, "GET", webhookWorkflowPath(slug, id)+"/workflow-receipts/"+url.PathEscape(eventID), nil, &out)
	return out, err
}
