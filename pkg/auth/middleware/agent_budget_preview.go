package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Budget previews (ADR-20261111114059, amendment 2026-11-13): a request sent
// with api.AgentPreviewHeader is priced like the real one and answered with
// api.AgentBudgetPreview; nothing is written, no approval is opened and no
// activity or undo is recorded. apid lets the header reach only routes that
// price every accepted request before writing (agentapproval.Previewable).

// Previewing reports whether r asks for a budget preview.
func Previewing(r *http.Request) bool {
	return r.Header.Get(api.AgentPreviewHeader) == api.AgentPreviewBudget
}

type previewNoteKey struct{}

// previewNote carries the approval the real request would wait for.
type previewNote struct{ category string }

// notePreviewApproval works out, without opening one, the approval an agent
// key's request would wait for. answered reports that it wrote an error.
func (m *Middleware) notePreviewApproval(w http.ResponseWriter, r *http.Request, key state.APIKey) (answered bool) {
	note := &previewNote{}
	*r = *r.WithContext(context.WithValue(r.Context(), previewNoteKey{}, note))
	rule, ok := agentapproval.Classify(r.Pattern)
	if !ok {
		return false
	}
	if m.RemoteAgentGate == nil {
		if skip, answered := m.ungatedCategory(w, r, key, rule); answered || skip {
			return answered
		}
	}
	if rule.Target != agentapproval.TargetNone {
		production, answered := m.productionTarget(w, r, rule.Target)
		if answered || !production {
			return answered
		}
	}
	note.category = rule.Category
	return false
}

// answerBudgetPreview writes what a change adding millicents would cost the
// calling credential.
func (m *Middleware) answerBudgetPreview(w http.ResponseWriter, r *http.Request, plan api.Plan, millicents int64) {
	out := api.AgentBudgetPreview{Preview: true}
	_, key, _, ok := PrincipalFrom(r)
	if ok && key != nil && ProjectLimitable(*key) {
		if !m.priceBudgetPreview(w, r, plan, *key, max(millicents, 0), &out) {
			return
		}
	}
	w.Header().Set(api.AgentPreviewHeader, api.AgentPreviewBudget)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

// priceBudgetPreview fills out for an agent key; false when it wrote an
// error instead.
func (m *Middleware) priceBudgetPreview(w http.ResponseWriter, r *http.Request, plan api.Plan, key state.APIKey, millicents int64, out *api.AgentBudgetPreview) bool {
	ctx := r.Context()
	now := time.Now()
	out.Charged = true
	out.AddedCents = api.CentsCeil(millicents)
	out.Month = state.BudgetMonth(now).Format("2006-01")
	if note, ok := ctx.Value(previewNoteKey{}).(*previewNote); ok && note.category != "" {
		out.NeedsApproval, out.ApprovalCategory = true, note.category
	}
	view, err := state.AgentBudgetFor(ctx, m.Authn, plan, key)
	if err != nil {
		m.storeUnavailable(w, r, err, "could not load the agent's budget; retry shortly")
		return false
	}
	var spent int64
	if budgets, ok := m.Authn.(state.AgentBudgetStore); ok {
		if spent, err = budgets.AgentBudgetSpent(ctx, key.ID, now); err != nil {
			m.storeUnavailable(w, r, err, "could not load the agent's budget; retry shortly")
			return false
		}
	}
	out.SpentCents = api.CentsCeil(spent)
	if !view.Limited {
		return true
	}
	budget := view.Cents
	remaining := max(budget-out.SpentCents, 0)
	out.BudgetCents, out.RemainingCents = &budget, &remaining
	// The approval a real request waits for first also consents to its
	// cost, so the budget is named only when nothing else asks.
	if millicents > 0 && spent+millicents > budget*api.MillicentsPerCent && !out.NeedsApproval {
		out.NeedsApproval, out.ApprovalCategory = true, api.AgentApprovalBudget
	}
	return true
}
