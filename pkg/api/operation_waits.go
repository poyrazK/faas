// adr: 1194
package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

const (
	OperationWaitTimer             = "timer"
	OperationWaitApproval          = "approval"
	OperationApproverAccount       = "account"
	OperationApproverCustomer      = "customer"
	OperationApprovalApproved      = "approved"
	OperationApprovalRejected      = "rejected"
	OperationFailureApprovalDenied = "approval_rejected"
	OperationFailureApprovalExpiry = "approval_expired"
)

// OperationApprovalSpec declares that a workflow callback step is an approval
// decided by the account or by the operation's customer.
type OperationApprovalSpec struct {
	Step     string `json:"step" yaml:"step" toml:"step"`
	Approver string `json:"approver" yaml:"approver" toml:"approver"`
}

// OperationWaitingState describes why a waiting operation holds no execution.
// Until is the timer's wake time or the approval deadline, when one exists.
type OperationWaitingState struct {
	Kind     string     `json:"kind"`
	Step     string     `json:"step"`
	Since    time.Time  `json:"since"`
	Until    *time.Time `json:"until,omitempty"`
	Approver string     `json:"approver,omitempty"`
	// Event names the signal an event wait expects (ADR-1197).
	Event string `json:"event,omitempty"`
}

type OperationApprovalRequest struct {
	ExpectedGeneration int    `json:"expected_generation"`
	Decision           string `json:"decision"`
	Comment            string `json:"comment,omitempty"`
}

// OperationApprovalActor is recorded with the decision. ID is the deciding
// API key or platform tenant token, never a secret.
type OperationApprovalActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// OperationApprovalPayload is the platform-written callback payload; generic
// callback completion is refused for operation-linked runs.
type OperationApprovalPayload struct {
	Approval OperationApprovalDecision `json:"approval"`
}

type OperationApprovalDecision struct {
	Step     string                 `json:"step"`
	Decision string                 `json:"decision"`
	Actor    OperationApprovalActor `json:"actor"`
	Comment  string                 `json:"comment,omitempty"`
}

func (c *Client) DecideOperationApproval(ctx context.Context, slug, id, step string, req OperationApprovalRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/approvals/"+url.PathEscape(step), req, &out)
	return out, err
}

func (c *Client) DecidePlatformTenantSelfOperationApproval(ctx context.Context, id, step string, req OperationApprovalRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operations/"+url.PathEscape(id)+"/approvals/"+url.PathEscape(step), req, &out)
	return out, err
}
