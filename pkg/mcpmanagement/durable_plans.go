package mcpmanagement

import (
	"errors"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type storedPlan struct {
	RegressionResult   *api.DebugRegressionReview
	BudgetResult       *api.EnvironmentRouteBudgets
	Plan               releasePlan
	AppHash            [32]byte
	LatestID           string
	Attempted          bool
	Result             *api.DeploymentResponse
	Promoted           *api.DeploymentResponse
	PromotionAttempted bool
	Review             *api.AppReleaseReview
}

func (m *management) restorePlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	var records map[string]storedPlan
	if err := m.opts.planStore.read("plans/"+m.opts.planNamespace, &records); err != nil {
		return err
	}
	if len(records) > maxReleasePlans {
		return errors.New("invalid stored plan capacity")
	}
	for id, record := range records {
		p := record.Plan
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		// Local deploy and env plans are memory-only (ADR-20261026141841).
		if p.LocalSource != nil || p.EnvChange != nil || p.Action == "local_deploy" || p.Action == "env_change" {
			return errors.New("stored local plan")
		}
		if id != p.ID || len(id) != 64 || p.APIBase != m.opts.APIBase || !m.appIDAllowed(p.AppID) || (p.Scope != api.DefaultEnvScope && (!api.ValidProjectEnvironmentSlug(p.Scope) || p.EnvironmentID == "" || p.ProjectID == "" || p.Project == "")) {
			return errors.New("stored release plan binding mismatch")
		}
		if p.Source != nil && ((p.Action != "deploy" && p.Action != "workload_rollout") || p.Image != "" || p.Source.AppID != p.AppID || !sourceCommit.MatchString(p.Source.CommitSHA) || !sourceRepository.MatchString(p.Source.Repo) || p.Source.RepositoryID <= 0 || p.Source.InstallationID <= 0 || record.Review == nil || !reflect.DeepEqual(p.Source.ReleaseReview, *record.Review)) {
			return errors.New("stored source plan binding mismatch")
		}
		if (p.Action == "workload_rollout" || p.Action == "service_abort") && (record.Review == nil || record.Review.Protected || !immutableImage.MatchString(p.Image) && p.Source == nil && p.Action == "workload_rollout" || p.RolloutMode != api.ExecutionModeService && p.RolloutMode != api.ExecutionModeWorker || p.Action == "service_abort" && p.RolloutMode != api.ExecutionModeService) {
			return errors.New("stored native rollout binding mismatch")
		}

		if p.Action == "route_budgets" {
			if p.Budgets == nil || routemonitor.ValidateEnvironmentBudgets(p.Budgets.Proposed) != nil || validateBudgetResponse(p.Budgets.Before, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil || p.Budgets.Proposed.AppID != p.AppID || p.Budgets.Proposed.ProjectID != p.ProjectID || p.Budgets.Proposed.EnvironmentID != p.EnvironmentID || *p.Budgets.Proposed.ExpectedRevision != p.Budgets.Before.Revision || *p.Budgets.Proposed.ExpectedAppRevision != p.Budgets.Before.AppRevision {
				return errors.New("stored route budget plan binding mismatch")
			}
			if record.BudgetResult != nil && validateBudgetResponse(*record.BudgetResult, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil {
				return errors.New("stored route budget receipt binding mismatch")
			}
		}
		if p.Action == "regression_action" {
			if validateRegressionPlan(&p) != nil {
				return errors.New("stored regression plan binding mismatch")
			}
			if record.RegressionResult != nil && validateRegressionReceipt(p.Regression, *record.RegressionResult) != nil {
				return errors.New("stored regression receipt binding mismatch")
			}
		}
		p.regressionResult = record.RegressionResult
		p.budgetResult = record.BudgetResult
		p.appHash, p.latestID, p.attempted = record.AppHash, record.LatestID, record.Attempted
		p.review = record.Review
		p.result, p.promoted, p.promotionAttempted = record.Result, record.Promoted, record.PromotionAttempted
		m.plans[id] = &p
	}
	return nil
}

// Called with the management lock held. Persist the attempt BEFORE the customer
// API write, so a process death after commit retries the original key and body.
func (m *management) persistPlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	records := make(map[string]storedPlan, len(m.plans))
	for id, p := range m.plans {
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		records[id] = storedPlan{RegressionResult: p.regressionResult, BudgetResult: p.budgetResult, Plan: *p, AppHash: p.appHash, LatestID: p.latestID, Attempted: p.attempted, Result: p.result, Promoted: p.promoted, PromotionAttempted: p.promotionAttempted, Review: p.review}
	}
	return m.opts.planStore.update("plans/"+m.opts.planNamespace, records)
}
