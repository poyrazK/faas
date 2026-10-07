// adr: 644
package api

import (
	"context"
	"net/http"
	"time"
)

type OperationSubmissionScope struct {
	AppID string `json:"app_id"`
	Scope string `json:"scope"`
	Name  string `json:"name"`
}

type OperationSubmissionLookupRequest struct {
	AppID            string                   `json:"app_id"`
	Scope            string                   `json:"scope"`
	Name             string                   `json:"name"`
	IdempotencyKey   string                   `json:"idempotency_key"`
	ExpectedIdentity *OperationTenantIdentity `json:"expected_identity,omitempty"`
}

// Unresolved is not evidence of rejection. Lookup never submits work.
type OperationSubmissionLookupResponse struct {
	AcceptedAt           *time.Time                 `json:"accepted_at,omitempty"`
	State                string                     `json:"state"`
	Receipt              *OperationAcceptedResponse `json:"receipt,omitempty"`
	IdempotencyExpiresAt *time.Time                 `json:"idempotency_expires_at,omitempty"`
}

func (c *Client) LookupPlatformTenantSelfOperationSubmission(ctx context.Context, req OperationSubmissionLookupRequest) (OperationSubmissionLookupResponse, error) {
	var out OperationSubmissionLookupResponse
	err := c.doOperation(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operations/submissions/lookup", req, &out)
	return out, err
}
