package mcpmanagement

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// Keep new action kinds out of namespaces validated by older bridges.
const budgetActionsNamespace = "budget-actions/"

type actionBudget struct {
	Change              string `json:"change"`
	ExpectedRevision    int64  `json:"expected_revision"`
	ExpectedAppRevision int64  `json:"expected_app_revision"`
}

func budgetActionPhase(p *releasePlan) (*actionBudget, actionPhase) {
	phase := actionPhase{State: "not_attempted"}
	if p.attempted {
		phase.State = "outcome_unknown"
	}
	if p.Budgets == nil {
		return nil, phase
	}
	req := p.Budgets.Proposed
	if req.ExpectedRevision == nil || req.ExpectedAppRevision == nil {
		return nil, phase
	}
	budget := &actionBudget{Change: "set_override", ExpectedRevision: *req.ExpectedRevision, ExpectedAppRevision: *req.ExpectedAppRevision}
	if req.Inherit {
		budget.Change = "inherit_app_policy"
	}
	if p.attempted && p.budgetResult != nil && validateBudgetReceipt(p, *p.budgetResult) == nil {
		out := p.budgetResult
		phase = actionPhase{State: "committed", Receipt: &actionReceipt{BudgetRevision: &out.Revision, AppPolicyRevision: &out.AppRevision, BudgetSource: out.BudgetSource}}
	}
	return budget, phase
}

func validateBudgetAction(entry actionRecord) error {
	b := entry.Budget
	if entry.Action == "route_budgets" {
		if b == nil || b.Change != "set_override" && b.Change != "inherit_app_policy" || !validBudgetRevision(b.ExpectedRevision) || !validBudgetRevision(b.ExpectedAppRevision) || entry.Workload != nil {
			return errors.New("invalid action budget review")
		}
	} else if b != nil {
		return errors.New("unexpected action budget review")
	}
	for _, phase := range []*actionPhase{&entry.Apply, entry.Promotion} {
		if phase == nil || phase.Receipt == nil {
			continue
		}
		r := phase.Receipt
		if entry.Action != "route_budgets" {
			if r.BudgetRevision != nil || r.AppPolicyRevision != nil || r.BudgetSource != "" {
				return errors.New("unexpected action budget receipt")
			}
			continue
		}
		source := "environment_policy"
		if b.Change == "inherit_app_policy" {
			source = "app_policy"
		}
		if phase.State != "committed" || r.BudgetRevision == nil || r.AppPolicyRevision == nil || !validBudgetRevision(*r.BudgetRevision) || !validBudgetRevision(*r.AppPolicyRevision) || *r.BudgetRevision != b.ExpectedRevision+1 || *r.AppPolicyRevision != b.ExpectedAppRevision || r.BudgetSource != source || r.DeploymentID != "" || r.RollbackOperationID != "" || r.RecoveryRequestID != "" || r.HandoffGeneration != 0 || r.RegressionVersion != "" || r.TriageAction != "" || r.TriageState != "" {
			return errors.New("invalid committed action budget receipt")
		}
	}
	return nil
}

func validBudgetRevision(revision int64) bool {
	return revision >= 0 && revision <= api.RouteRequirementsMaxRevision
}

func budgetActionObservation(entry actionRecord) *actionObservation {
	if entry.Action != "route_budgets" {
		return nil
	}
	environment := entry.Scope
	if environment == api.DefaultEnvScope {
		environment = ""
	}
	return &actionObservation{Tool: "gregale_get_route_budgets", Arguments: actionObservationArgs{App: entry.App, Project: entry.Project, Environment: environment}}
}
