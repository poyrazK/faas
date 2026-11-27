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

// ChargesAgentBudget reports whether the request's credential pays from an
// agent budget, so a handler can skip pricing work for any other caller.
func (m *Middleware) ChargesAgentBudget(r *http.Request) bool {
	_, key, _, ok := PrincipalFrom(r)
	return ok && key != nil && ProjectLimitable(*key)
}

// ChargeAgentBudget prices a change an agent key is about to make: one that
// adds millicents of committed cost to plan's account (ADR-20261111114059).
// Within the key's monthly budget, or when the request went through on a
// person's approval, it returns a commit the handler calls once the change
// is written. Past the budget it opens a budget approval and answers 428;
// answered is then true and the handler must stop. Other credentials and
// changes that add nothing are never charged.
func (m *Middleware) ChargeAgentBudget(w http.ResponseWriter, r *http.Request, plan api.Plan, millicents int64) (commit func(), answered bool) {
	noop := func() {}
	if Previewing(r) {
		m.answerBudgetPreview(w, r, plan, millicents)
		return noop, true
	}
	acct, key, _, ok := PrincipalFrom(r)
	if !ok || key == nil || !ProjectLimitable(*key) || millicents == 0 {
		return noop, false
	}
	budgets, ok := m.Authn.(state.AgentBudgetStore)
	if !ok {
		return noop, false
	}
	keyID := key.ID
	// add records a signed change to the key's standing cost once the
	// handler wrote it: a change that lowers cost gives budget back
	// (cost-control review 2026-11-19, N10).
	add := func(delta int64) func() { //nolint:contextcheck // the spend outlives the request, like the activity row.
		return func() { //nolint:contextcheck // the spend outlives the request, like the activity row.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := budgets.AddAgentBudgetSpend(ctx, keyID, time.Now(), delta); err != nil && m.Log != nil {
				m.Log.Warn("agent_budget.record_failed", "key_id", keyID, "millicents", delta, "error", err.Error())
			}
		}
	}
	if millicents < 0 {
		return add(millicents), false
	}
	commit = add(millicents)
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
	if note != nil {
		// Reserve before the handler writes (N9): two concurrent requests
		// can no longer both pass a check of the same total. The middleware
		// releases the reservation after the handler unless it commits.
		reserved, err := budgets.ReserveAgentBudget(ctx, keyID, millicents, view.Cents*api.MillicentsPerCent)
		if err != nil {
			m.storeUnavailable(w, r, err, "could not load the agent's budget; retry shortly")
			return noop, true
		}
		if reserved {
			note.reservedBudget += millicents
			return func() { note.reservedBudget -= millicents }, false
		}
	}
	spent, err := budgets.AgentBudgetSpent(ctx, key.ID, time.Now())
	if err != nil {
		m.storeUnavailable(w, r, err, "could not load the agent's budget; retry shortly")
		return noop, true
	}
	if note == nil && spent+millicents <= view.Cents*api.MillicentsPerCent {
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

// releaseAgentBudget gives back budget a handler reserved but did not keep:
// it refused the change, failed, or answered before writing (cost-control
// review 2026-11-19, N9).
func (m *Middleware) releaseAgentBudget(keyID string, note *agentActivityNote) {
	if note == nil || note.reservedBudget <= 0 {
		return
	}
	budgets, ok := m.Authn.(state.AgentBudgetStore)
	if !ok {
		return
	}
	released := note.reservedBudget
	note.reservedBudget = 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := budgets.AddAgentBudgetSpend(ctx, keyID, time.Now(), -released); err != nil && m.Log != nil {
		m.Log.Warn("agent_budget.release_failed", "key_id", keyID, "millicents", released, "error", err.Error())
	}
}

// usd formats cents as dollars.
func usd(cents int64) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
