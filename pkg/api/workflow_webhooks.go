package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

type PutWebhookAutomationBindingRequest struct {
	ExpectedVersion  *int64          `json:"expected_version"`
	WorkflowName     string          `json:"workflow_name"`
	EventType        string          `json:"event_type"`
	Filter           json.RawMessage `json:"filter,omitempty"`
	TakeOverDelivery bool            `json:"take_over_delivery"`
}
type WebhookAutomationBindingResponse struct {
	EndpointID   string          `json:"endpoint_id"`
	WorkflowName string          `json:"workflow_name"`
	EventType    string          `json:"event_type"`
	Filter       json.RawMessage `json:"filter"`
	Version      int64           `json:"version"`
	UpdatedAt    string          `json:"updated_at"`
}
type WebhookAutomationReceiptResponse struct {
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

func webhookAutomationPath(slug, id string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/inbound-webhooks/" + url.PathEscape(id)
}
func (c *Client) PutWebhookAutomationBinding(ctx context.Context, slug, id string, body PutWebhookAutomationBindingRequest) (WebhookAutomationBindingResponse, error) {
	var out WebhookAutomationBindingResponse
	err := c.do(ctx, "PUT", webhookAutomationPath(slug, id)+"/automation-binding", body, &out)
	return out, err
}
func (c *Client) GetWebhookAutomationBinding(ctx context.Context, slug, id string) (WebhookAutomationBindingResponse, error) {
	var out WebhookAutomationBindingResponse
	err := c.do(ctx, "GET", webhookAutomationPath(slug, id)+"/automation-binding", nil, &out)
	return out, err
}
func (c *Client) DeleteWebhookAutomationBinding(ctx context.Context, slug, id string, version int64) error {
	return c.do(ctx, "DELETE", webhookAutomationPath(slug, id)+"/automation-binding?expected_version="+strconv.FormatInt(version, 10), nil, nil)
}
func (c *Client) GetWebhookAutomationReceipt(ctx context.Context, slug, id, eventID string) (WebhookAutomationReceiptResponse, error) {
	var out WebhookAutomationReceiptResponse
	err := c.do(ctx, "GET", webhookAutomationPath(slug, id)+"/automation-receipts/"+url.PathEscape(eventID), nil, &out)
	return out, err
}
