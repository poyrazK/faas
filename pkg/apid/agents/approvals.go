package agents

import (
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Run approvals (ADR-20261027090016 amendment 2): a run's call to a tool in
// approve mode waits in outboundd until the seller decides here.

// ListRunApprovals handles GET /v1/agent-runs/{id}/approvals.
func (h *Handler) ListRunApprovals(w http.ResponseWriter, r *http.Request, acct state.Account) {
	approvals, err := h.Store.RunApprovals(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.get", err))
		return
	}
	now := h.now()
	out := api.AgentRunApprovalList{Items: make([]api.AgentRunApproval, 0, len(approvals))}
	for _, a := range approvals {
		out.Items = append(out.Items, approvalJSON(a, now))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// DecideRunApproval handles POST /v1/agent-runs/{id}/approvals/{approval_id}:
// the seller approves or denies one waiting call, once.
func (h *Handler) DecideRunApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var body api.DecideAgentRunApprovalRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, api.AgentRequestMaxBytes) {
		return
	}
	if prob := validDecision(body); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	approval, err := h.Store.DecideRunApproval(r.Context(), acct.ID, r.PathValue("id"), r.PathValue("approval_id"),
		body.Decision, body.Note, decidedBy(r))
	if errors.Is(err, agentstore.ErrApprovalClosed) {
		state := approval.State(h.now())
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeAgentRunApprovalClosed, "Approval closed",
			fmt.Sprintf("the approval is %s; a decision is accepted only while it is pending", state)))
		return
	}
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "agent_run.approval", err))
		return
	}
	h.Audit(r.Context(), "agent_run.approval_decided", acct.ID, map[string]any{"run_id": approval.RunID,
		"approval_id": approval.ID, "tool": approval.Tool, "decision": body.Decision})
	httpkit.WriteJSON(w, http.StatusOK, approvalJSON(approval, h.now()))
}

func validDecision(body api.DecideAgentRunApprovalRequest) *api.Problem {
	switch {
	case body.Decision != api.AgentRunApprovalDecisionApprove && body.Decision != api.AgentRunApprovalDecisionDeny:
		return api.ErrValidation(fmt.Sprintf("decision must be %q or %q", api.AgentRunApprovalDecisionApprove, api.AgentRunApprovalDecisionDeny))
	case len(body.Note) > api.AgentRunApprovalNoteMaxBytes || !utf8.ValidString(body.Note):
		return api.ErrValidation(fmt.Sprintf("note must be valid text of at most %d bytes", api.AgentRunApprovalNoteMaxBytes))
	}
	return nil
}

// decidedBy names who decided: the API key, or the signed-in account.
func decidedBy(r *http.Request) string {
	if p, ok := httpkit.PrincipalFrom(r); ok && p.Key != nil {
		return "api_key:" + p.Key.ID
	}
	return "account"
}

func approvalJSON(a agentstore.Approval, now time.Time) api.AgentRunApproval {
	return api.AgentRunApproval{ID: a.ID, RunID: a.RunID, Tool: a.Tool, ArgumentsSHA256: a.ArgumentsSHA256,
		ArgumentsSummary: a.ArgumentsSummary, State: a.State(now), Note: a.Note, CreatedAt: a.CreatedAt,
		ExpiresAt: a.ExpiresAt, DecidedAt: a.DecidedAt}
}
