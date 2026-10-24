package mcpmanagement

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type actionOutcome struct {
	PlanID           string         `json:"plan_id"`
	Action           string         `json:"action"`
	ObservedAt       time.Time      `json:"observed_at"`
	Outcome          string         `json:"outcome"`
	Reason           string         `json:"reason,omitempty"`
	Receipt          *actionReceipt `json:"receipt,omitempty"`
	NativeCompletion string         `json:"native_completion"`
	Instruction      string         `json:"instruction"`
}

func registerActionOutcomes(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_action_outcome", "Read a retained route-budget action's native durable receipt, including after original plan expiry. Requires current immutable target/read consent. A matching request fingerprint confirms historical policy intent only; missing or unavailable evidence remains unresolved. Never replays a write, changes history or renews expiry. Other action kinds remain unsupported.", m.getActionOutcome)
}

func (m *management) getActionOutcome(ctx context.Context, args getActionArgs) (any, error) {
	value, err := m.getAction(ctx, args)
	if err != nil {
		return nil, err
	}
	history := value.(*guardedRead)
	entry := history.value.(actionHistoryDetail).Action.actionRecord
	m.mu.Lock()
	lookup, found := m.budgetOutcomes[args.PlanID]
	m.mu.Unlock()
	out := actionOutcome{PlanID: entry.PlanID, Action: entry.Action, ObservedAt: time.Now().UTC(), Outcome: "unresolved", NativeCompletion: "not_observed", Instruction: "A verified receipt confirms historical policy intent only, not current configuration or runtime health. Missing evidence never proves non-commit. Inspect native state; never replace an ambiguous original request or renew its write authority."}
	switch {
	case entry.Action != "route_budgets":
		out.Reason = "unsupported_action"
	case entry.Apply.State == "not_attempted":
		out.Reason = "no_recorded_attempt"
	case !found:
		out.Reason = "lookup_unavailable"
	case !validBudgetOutcomeLookup(lookup) || lookup.Binding != budgetOutcomeBinding(entry):
		return nil, invalid("state_unavailable", "budget outcome reference is invalid")
	default:
		m.readBudgetOutcome(ctx, entry, lookup, &out)
	}
	return &guardedRead{value: out, check: func(ctx context.Context) error {
		if err := history.check(ctx); err != nil {
			return err
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		current, ok := m.actions[entry.PlanID]
		if !ok || !time.Now().Before(current.RetainedUntil) || budgetOutcomeBinding(current) != budgetOutcomeBinding(entry) {
			return invalid("action_not_found", "original action is no longer retained")
		}
		if found && m.budgetOutcomes[entry.PlanID] != lookup {
			return invalid("state_unavailable", "original outcome reference changed during read")
		}
		return nil
	}}, nil
}

func (m *management) readBudgetOutcome(ctx context.Context, entry actionRecord, lookup budgetOutcomeLookup, out *actionOutcome) {
	environment := entry.Scope
	if environment == api.DefaultEnvScope {
		environment = ""
	}
	receipt, err := m.client.GetEnvironmentRouteBudgetReceipt(ctx, entry.App, environment, lookup.RequestID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if !budgetOutcomeMatches(entry, lookup, receipt) {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{BudgetRevision: &receipt.Revision, AppPolicyRevision: &receipt.AppRevision, BudgetSource: receipt.BudgetSource}
}

func budgetOutcomeMatches(entry actionRecord, lookup budgetOutcomeLookup, r api.EnvironmentRouteBudgetReceipt) bool {
	if entry.Budget == nil {
		return false
	}
	source := "environment_policy"
	if entry.Budget.Change == "inherit_app_policy" {
		source = "app_policy"
	}
	return r.Revision > 0 && validBudgetRevision(r.Revision) && validBudgetRevision(r.AppRevision) && r.RequestID == lookup.RequestID && r.RequestHash == lookup.RequestHash && r.AppID == entry.AppID && r.Scope == entry.Scope && r.ProjectID == entry.ProjectID && r.EnvironmentID == entry.EnvironmentID && r.Revision == entry.Budget.ExpectedRevision+1 && r.AppRevision == entry.Budget.ExpectedAppRevision && r.BudgetSource == source
}
