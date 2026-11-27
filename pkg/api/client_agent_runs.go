package api

import (
	"context"
	"net/url"
)

// Agent run approvals (ADR-20261027090016 amendment 2): a run's call to a
// tool in approve mode waits until the seller decides it.

// ListAgentRunApprovals lists a run's approvals, oldest first.
func (c *Client) ListAgentRunApprovals(ctx context.Context, runID string) (AgentRunApprovalList, error) {
	var out AgentRunApprovalList
	return out, c.do(ctx, "GET", "/v1/agent-runs/"+url.PathEscape(runID)+"/approvals", nil, &out)
}

// DecideAgentRunApproval approves or denies one pending approval of a run.
// An approval already decided or expired answers 409
// CodeAgentRunApprovalClosed.
func (c *Client) DecideAgentRunApproval(ctx context.Context, runID, approvalID string, req DecideAgentRunApprovalRequest) (AgentRunApproval, error) {
	var out AgentRunApproval
	return out, c.do(ctx, "POST", "/v1/agent-runs/"+url.PathEscape(runID)+"/approvals/"+url.PathEscape(approvalID), req, &out)
}
