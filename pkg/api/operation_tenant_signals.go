// adr: 20261017100853
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Who delivered a signal; the Operation's signal_received event records it.
const (
	OperationSignalSourceAccount  = "account"
	OperationSignalSourceCustomer = "customer"
	OperationSignalSourceWebhook  = "webhook"
)

// PlatformTenantSubjectSignalRequest signals the authenticated customer's own
// active Operations of one definition about one business subject. The
// definition fixes the app, environment and Operation name.
type PlatformTenantSubjectSignalRequest struct {
	DefinitionID string           `json:"definition_id"`
	Subject      OperationSubject `json:"subject"`
	SignalID     string           `json:"signal_id"`
	Event        string           `json:"event"`
	Payload      json.RawMessage  `json:"payload,omitempty"`
}

func (c *Client) SignalPlatformTenantSelfOperation(ctx context.Context, id string, req OperationSignalRequest) (OperationSignalReceipt, error) {
	var out OperationSignalReceipt
	err := c.do(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operations/"+url.PathEscape(id)+"/signals", req, &out)
	return out, err
}

func (c *Client) SignalPlatformTenantSelfOperationsBySubject(ctx context.Context, req PlatformTenantSubjectSignalRequest) (OperationSubjectSignalResponse, error) {
	var out OperationSubjectSignalResponse
	err := c.do(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operation-signals", req, &out)
	return out, err
}
