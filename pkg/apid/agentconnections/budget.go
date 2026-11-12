package agentconnections

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261111114059: an agent connection's monthly budget for committed
// cost.

// Budget is GET /v1/agent-connections/{id}/budget: the budget of one of the
// caller's agents and what it committed this month. An agent key reads only
// its own.
func Budget(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		k, ok := d.budgetConnection(w, r, acct)
		if !ok {
			return
		}
		writeBudget(w, r, d.Store, k)
	}
}

// SelfBudget is GET /v1/agent-connections/self/budget: an agent reads its
// own budget, so it can plan within it.
func SelfBudget(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		key, isAgent := agentKey(r)
		if !isAgent {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agent key required", "only an agent's own key can read its budget here; people use /v1/agent-connections/{id}/budget"))
			return
		}
		writeBudget(w, r, d.Store, *key)
	}
}

// SetBudget is PUT /v1/agent-connections/{id}/budget: the person who
// connected the agent sets its monthly budget, makes it unlimited (within
// the organization's ceiling) or returns it to the plan default.
func SetBudget(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if _, isAgent := agentKey(r); isAgent {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agent key cannot change budgets", "a person sets an agent's budget"))
			return
		}
		k, ok := d.budgetConnection(w, r, acct)
		if !ok {
			return
		}
		var req api.AgentBudgetRequest
		if err := httpkit.DecodeJSON(r, &req); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid budget request"))
			return
		}
		budget, problem := budgetFromRequest(req)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		budgets, ok := d.Store.(state.AgentBudgetStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("agent budgets are unavailable"))
			return
		}
		if err := budgets.SetAgentKeyBudget(r.Context(), k.ID, budget, acct.ID); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not save the agent's budget"))
			return
		}
		if d.Audit != nil {
			d.Audit(r.Context(), "agent.budget_set", acct.ID, map[string]any{"key_id": k.ID, "set": budget.Set, "unlimited": budget.Unlimited, "monthly_cents": budget.MonthlyCents})
		}
		writeBudget(w, r, d.Store, k)
	}
}

func (d Deps) budgetConnection(w http.ResponseWriter, r *http.Request, acct state.Account) (state.APIKey, bool) {
	id := r.PathValue("id")
	if key, isAgent := agentKey(r); isAgent && key.ID != id {
		connectionNotFound(w)
		return state.APIKey{}, false
	}
	k, err := ownConnection(r.Context(), d.Store, acct.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		connectionNotFound(w)
		return state.APIKey{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the agent connection"))
		return state.APIKey{}, false
	}
	return k, true
}

// budgetFromRequest accepts exactly one of monthly_cents, unlimited and
// default.
func budgetFromRequest(req api.AgentBudgetRequest) (state.AgentKeyBudget, *api.Problem) {
	chosen := 0
	if req.MonthlyCents != nil {
		chosen++
	}
	if req.Unlimited {
		chosen++
	}
	if req.Default {
		chosen++
	}
	if chosen != 1 {
		return state.AgentKeyBudget{}, api.ErrValidation("set exactly one of monthly_cents, unlimited or default")
	}
	switch {
	case req.Default:
		return state.AgentKeyBudget{}, nil
	case req.Unlimited:
		return state.AgentKeyBudget{Set: true, Unlimited: true}, nil
	}
	if c := *req.MonthlyCents; c < 0 || c > api.MaxAgentBudgetCents {
		return state.AgentKeyBudget{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid budget",
			"monthly_cents must be between 0 and the maximum").WithLimit(api.MaxAgentBudgetCents, c)
	}
	return state.AgentKeyBudget{Set: true, MonthlyCents: *req.MonthlyCents}, nil
}

// writeBudget answers the budget that applies to the key, priced at the
// plan of the account it acts for.
func writeBudget(w http.ResponseWriter, r *http.Request, store state.Store, k state.APIKey) {
	ctx := r.Context()
	owner, err := state.AppResourceAccount(ctx, store, state.App{AccountID: k.AccountID, OrgID: k.OrgID})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the agent's plan"))
		return
	}
	view, err := state.AgentBudgetFor(ctx, store, owner.Plan, k)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the agent's budget"))
		return
	}
	now := time.Now()
	var spent int64
	if budgets, ok := store.(state.AgentBudgetStore); ok {
		if spent, err = budgets.AgentBudgetSpent(ctx, k.ID, now); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load the agent's spend"))
			return
		}
	}
	out := api.AgentBudgetResponse{Source: view.Source, KeyUnlimited: view.Own.Set && view.Own.Unlimited, OrgMaxCents: view.OrgMax,
		SpentCents: api.CentsCeil(spent), Month: state.BudgetMonth(now).Format("2006-01")}
	if view.Limited {
		cents := view.Cents
		out.MonthlyCents = &cents
	}
	if view.Own.Set && !view.Own.Unlimited {
		cents := view.Own.MonthlyCents
		out.KeyMonthlyCents = &cents
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
