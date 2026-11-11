package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ChargeAgentBudget prices a change an agent key is about to make: one that
// adds millicents of committed cost to plan's account (ADR-20261111114059).
// Within the key's monthly budget, or when the request went through on a
// person's approval, it returns a commit the handler calls once the change
// is written. Past the budget it opens a budget approval and answers 428;
// answered is then true and the handler must stop. Other credentials and
// changes that add nothing are never charged.
func (m *Middleware) ChargeAgentBudget(w http.ResponseWriter, r *http.Request, plan api.Plan, millicents int64) (commit func(), answered bool) {
	noop := func() {}
	acct, key, _, ok := PrincipalFrom(r)
	if !ok || key == nil || !ProjectLimitable(*key) || millicents <= 0 {
		return noop, false
	}
	budgets, ok := m.Authn.(state.AgentBudgetStore)
	if !ok {
		return noop, false
	}
	keyID := key.ID
	commit = func() { //nolint:contextcheck // the spend outlives the request, like the activity row.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := budgets.AddAgentBudgetSpend(ctx, keyID, time.Now(), millicents); err != nil && m.Log != nil {
			m.Log.Warn("agent_budget.record_failed", "key_id", keyID, "millicents", millicents, "error", err.Error())
		}
	}
	ctx := r.Context()
	note, _ := ctx.Value(agentActivityNoteKey{}).(*agentActivityNote)
	if note != nil && note.approvalID != "" {
		// A person approved exactly this request.
		return commit, false
	}
	view, err := state.AgentBudgetFor(ctx, m.Authn, plan, *key)
	if err != nil {
		m.storeUnavailable(w, r, err, "could not load the agent's budget; retry shortly")
		return noop, true
	}
	if !view.Limited {
		return commit, false
	}
	spent, err := budgets.AgentBudgetSpent(ctx, key.ID, time.Now())
	if err != nil {
		m.storeUnavailable(w, r, err, "could not load the agent's budget; retry shortly")
		return noop, true
	}
	if spent+millicents <= view.Cents*api.MillicentsPerCent {
		return commit, false
	}
	gate, ok := m.Authn.(AgentApprovalGate)
	if !ok || note == nil || note.binding == nil {
		// Without the binding taken before the handler read the body, an
		// approval could not be tied to this exact change.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Over the agent's budget",
			fmt.Sprintf("this change adds %s a month, past this agent's budget of %s (%s used); ask a person to raise it",
				usd(api.CentsCeil(millicents)), usd(view.Cents), usd(api.CentsCeil(spent)))).
			WithLimit(view.Cents, api.CentsCeil(spent+millicents)))
		return noop, true
	}
	detail := fmt.Sprintf("this change adds about %s a month, past this agent's monthly budget of %s (%s used), so it needs a person's approval",
		usd(api.CentsCeil(millicents)), usd(view.Cents), usd(api.CentsCeil(spent)))
	reason := api.SanitizeAgentReason(r.Header.Get(api.AgentReasonHeader))
	decision, err := m.decideBudget(ctx, gate, acct, *key, *note.binding, reason, millicents, detail)
	if err != nil {
		m.storeUnavailable(w, r, err, "could not check the request's approval; retry shortly")
		return noop, true
	}
	if !decision.Allow {
		api.WriteProblem(w, decision.Problem)
		return noop, true
	}
	noteApprovalUsed(ctx, decision.ApprovalID)
	return commit, false
}

// decideBudget allows an over-budget change on a person's approval of
// exactly this request, answers an agent repeating a denied or expired one,
// or opens a budget approval request.
func (m *Middleware) decideBudget(ctx context.Context, gate AgentApprovalGate, acct state.Account, key state.APIKey, binding state.AgentApprovalBinding, reason string, cost int64, why string) (AgentGateDecision, error) {
	if approvalID, used, err := gate.ConsumeAgentApproval(ctx, binding); err != nil {
		return AgentGateDecision{}, gateUnavailable("could not check the request's approval; retry shortly", err)
	} else if used {
		return AgentGateDecision{Allow: true, ApprovalID: approvalID}, nil
	}
	outcome, decided, err := gate.RecentAgentApprovalOutcome(ctx, binding, api.AgentApprovalOutcomeWindow)
	if err != nil {
		return AgentGateDecision{}, gateUnavailable("could not check the request's approval; retry shortly", err)
	}
	if decided {
		return AgentGateDecision{Problem: m.approvalOutcomeProblem(acct, key, outcome)}, nil
	}
	return m.requestApproval(ctx, gate, acct, key, agentapproval.Rule{Category: api.AgentApprovalBudget}, binding, reason, cost, why)
}

// usd formats cents as dollars.
func usd(cents int64) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
