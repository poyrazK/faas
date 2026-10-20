// adr: 1193
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// PutOperationWebhookBindingRequest routes a verified inbound webhook endpoint
// to a customer operation. The platform tenant is the operation's owner; it is
// fixed here and never derived from provider payload. TakeOverDelivery must be
// true: bound events no longer reach the endpoint's plain delivery path.
//
// Mode "signal" (ADR-20261016100929) delivers mapped provider events to the tenant's
// waiting Operations whose subject the payload names, instead of starting
// Operations; EventTypes is then empty and Signals is the filter.
type PutOperationWebhookBindingRequest struct {
	ExpectedVersion  *int64                   `json:"expected_version"`
	Scope            string                   `json:"scope,omitempty"`
	Operation        string                   `json:"operation"`
	PlatformTenantID string                   `json:"platform_tenant_id"`
	EventTypes       []string                 `json:"event_types,omitempty"`
	TakeOverDelivery bool                     `json:"take_over_delivery"`
	Mode             string                   `json:"mode,omitempty"`
	Subject          *OperationSubjectSpec    `json:"subject,omitempty"`
	Signals          []OperationWebhookSignal `json:"signals,omitempty"`
}

// OperationWebhookSignal maps one provider event type to the Operation event
// a waiting workflow step expects.
type OperationWebhookSignal struct {
	ProviderEvent string `json:"provider_event"`
	Event         string `json:"event"`
}

const (
	OperationWebhookModeStart  = "start"
	OperationWebhookModeSignal = "signal"
	// Receipt statuses of a signal-mode delivery.
	InboundWebhookSignaled  = "signaled"
	InboundWebhookUnmatched = "unmatched"
)

type OperationWebhookBindingResponse struct {
	EndpointID       string                   `json:"endpoint_id"`
	AppID            string                   `json:"app_id"`
	Scope            string                   `json:"scope"`
	Operation        string                   `json:"operation"`
	PlatformTenantID string                   `json:"platform_tenant_id"`
	EventTypes       []string                 `json:"event_types"`
	Mode             string                   `json:"mode"`
	Subject          *OperationSubjectSpec    `json:"subject,omitempty"`
	Signals          []OperationWebhookSignal `json:"signals,omitempty"`
	Version          int64                    `json:"version"`
	CreatedAt        string                   `json:"created_at"`
	UpdatedAt        string                   `json:"updated_at"`
}

// OperationWebhookInput is the deterministic operation input built from one
// verified event. It carries no receipt time, so a provider retry is identical.
type OperationWebhookInput struct {
	Webhook OperationWebhookEvent `json:"webhook"`
	Payload json.RawMessage       `json:"payload"`
}

type OperationWebhookEvent struct {
	Endpoint  string `json:"endpoint"`
	Provider  string `json:"provider"`
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
}

func operationWebhookBindingPath(slug, endpointID string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/inbound-webhooks/" + url.PathEscape(endpointID) + "/operation-binding"
}

func (c *Client) PutOperationWebhookBinding(ctx context.Context, slug, endpointID string, req PutOperationWebhookBindingRequest) (OperationWebhookBindingResponse, error) {
	var out OperationWebhookBindingResponse
	err := c.do(ctx, http.MethodPut, operationWebhookBindingPath(slug, endpointID), req, &out)
	return out, err
}

func (c *Client) GetOperationWebhookBinding(ctx context.Context, slug, endpointID string) (OperationWebhookBindingResponse, error) {
	var out OperationWebhookBindingResponse
	err := c.do(ctx, http.MethodGet, operationWebhookBindingPath(slug, endpointID), nil, &out)
	return out, err
}

func (c *Client) DeleteOperationWebhookBinding(ctx context.Context, slug, endpointID string, expectedVersion int64) error {
	return c.do(ctx, http.MethodDelete, operationWebhookBindingPath(slug, endpointID)+"?expected_version="+strconv.FormatInt(expectedVersion, 10), nil, nil)
}
