package mcpmanagement

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type pendingBrowserApproval struct {
	PlanID        string `json:"plan_id"`
	Status        string `json:"status"`
	ReviewURL     string `json:"review_url"`
	ExpiresAt     string `json:"expires_at"`
	RetryAfterSec int    `json:"retry_after_seconds"`
	Instruction   string `json:"instruction"`
}

// Called under the plan lock. Only the human review link escapes to MCP;
// polling material and the approval credential remain private and encrypted.
func (m *management) browserEnvironmentApproval(ctx context.Context, p *environmentPlan) (any, error) {
	if p.approvalRequest == nil {
		request, err := m.client.RequestBrowserPromotionApproval(ctx, p.Project, p.Environment, api.BrowserPromotionApprovalRequest{PromotionToken: p.token, Reason: p.Reason})
		if err != nil {
			return nil, err
		}
		u, parseErr := url.Parse(request.ReviewURL)
		expiry, expiryErr := time.Parse(time.RFC3339Nano, request.ExpiresAt)
		if parseErr != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname()))) || expiryErr != nil || !expiry.After(time.Now()) || request.Ticket == "" || request.ExchangeCode == "" {
			return nil, invalid("invalid_response", "Gregale could not provide a valid human review request")
		}
		p.approvalRequest = &request
		if err := m.persistEnvironmentPlans(); err != nil {
			return nil, err
		}
		return browserApprovalPending(p), nil
	}
	expiry, err := time.Parse(time.RFC3339Nano, p.approvalRequest.ExpiresAt)
	if err != nil || !expiry.After(time.Now()) {
		return nil, invalid("approval_expired", "human review expired; create a new environment promotion plan")
	}
	approval, err := m.client.ExchangeBrowserPromotionApproval(ctx, p.Project, p.Environment, api.BrowserPromotionApprovalExchange{Ticket: p.approvalRequest.Ticket, ExchangeCode: p.approvalRequest.ExchangeCode})
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeBrowserApprovalPending {
		return browserApprovalPending(p), nil
	}
	if err != nil {
		// Exchange failures must never reflect private polling or approval data.
		return nil, invalid("approval_unavailable", "Gregale could not verify human approval; retry the plan or create a new plan if the review expired")
	}
	approvalExpiry, expiryErr := time.Parse(time.RFC3339, approval.ExpiresAt)
	if approval.Environment != p.Environment || approval.TokenKind != "promotion" || approval.ApprovalToken == "" || expiryErr != nil || !approvalExpiry.After(time.Now()) {
		return nil, invalid("invalid_response", "Gregale could not verify the approved promotion identity")
	}
	p.approval = approval.ApprovalToken
	return nil, m.persistEnvironmentPlans()
}

func browserApprovalPending(p *environmentPlan) pendingBrowserApproval {
	return pendingBrowserApproval{PlanID: p.ID, Status: "approval_pending", ReviewURL: p.approvalRequest.ReviewURL, ExpiresAt: p.approvalRequest.ExpiresAt, RetryAfterSec: 5, Instruction: "Ask the human to open review_url and approve this exact promotion in Gregale. Retry apply with the same plan_id after approval; do not send approval credentials to the agent."}
}
