package mcpmanagement

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type storedPlan struct {
	Failure            *planFailure          `json:",omitempty"`
	ConfigReceipt      *api.EnvBatchResponse `json:",omitempty"`
	Provision          *nativeProvision      `json:",omitempty"`
	OperationRequest   json.RawMessage       `json:",omitempty"`
	LocalReceipt       json.RawMessage       `json:",omitempty"`
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
		// Older bridges accidentally journaled temporary plans. Ignore these
		// records: source files and variable values cannot survive a restart.
		// A completed receipt, stripped of its temporary inputs, is restorable.
		if temporaryPlan(&p) && len(record.LocalReceipt) == 0 && record.Provision == nil {
			continue
		}
		if p.LocalSource != nil || p.EnvChange != nil || p.Provision != nil {
			return errors.New("stored temporary plan inputs")
		}
		if record.Provision != nil {
			if record.Provision.CancelRequested && record.Provision.Phase != "completed" {
				record.Provision.Phase = "cancelled"
			}
			if problem := record.Provision.Problem; problem != nil {
				problem.SchemaVersion = recoveryContractVersion
				problem.Recovery = recoveryForError(*problem)
			}
			if !record.Provision.valid(&p) {
				return errors.New("stored native provisioning binding mismatch")
			}
			p.Provision = &provisionPlan{Kind: record.Provision.Kind, Native: record.Provision, Resumable: true}
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
			if record.BudgetResult != nil && (!record.Attempted || validateBudgetReceipt(&p, *record.BudgetResult) != nil) {
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
		if record.Failure != nil {
			if record.Failure.Status != "refused" && record.Failure.Status != "partial" && record.Failure.Status != "outcome_unknown" {
				return errors.New("stored operation outcome mismatch")
			}
			if record.Failure.Problem.SchemaVersion != recoveryContractVersion || record.Failure.Problem.Outcome != record.Failure.Status || (record.Failure.Status == "partial") != (record.ConfigReceipt != nil) {
				return errors.New("stored operation receipt mismatch")
			}
			p.failure = record.Failure
		}
		if record.ConfigReceipt != nil {
			if !validConfigCheckpoint(&p, record.ConfigReceipt) {
				return errors.New("stored configuration checkpoint mismatch")
			}
			p.configReceipt = record.ConfigReceipt
		}
		if p.Operation != nil {
			if !operationKinds[p.Operation.Kind] || p.Action != "operation_"+p.Operation.Kind || !json.Valid(record.OperationRequest) {
				return errors.New("stored operation binding mismatch")
			}
			p.Operation.Request = record.OperationRequest
		}
		if len(record.LocalReceipt) > 0 {
			p.localReceipt = record.LocalReceipt
		}
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
	if err := m.syncActionHistory(time.Now().UTC()); err != nil {
		return err
	}
	if m.opts.planStore == nil {
		return nil
	}
	environments, err := m.storedEnvironmentPlans()
	if err != nil {
		return err
	}
	records := make(map[string]storedPlan, len(m.plans))
	for id, p := range m.plans {
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		if temporaryPlan(p) && p.localReceipt == nil {
			continue
		}
		plan := *p
		plan.LocalSource, plan.EnvChange, plan.Provision = nil, nil, nil
		var receipt json.RawMessage
		if p.localReceipt != nil {
			var err error
			receipt, err = json.Marshal(p.localReceipt)
			if err != nil {
				return err
			}
		}
		records[id] = storedPlan{Failure: p.failure, ConfigReceipt: p.configReceipt, LocalReceipt: receipt, RegressionResult: p.regressionResult, BudgetResult: p.budgetResult, Plan: plan, AppHash: p.appHash, LatestID: p.latestID, Attempted: p.attempted, Result: p.result, Promoted: p.promoted, PromotionAttempted: p.promotionAttempted, Review: p.review}
		if p.Operation != nil {
			r := records[id]
			r.OperationRequest = p.Operation.Request
			records[id] = r
		}
		if p.Provision != nil && p.Provision.Native != nil {
			r := records[id]
			r.Provision = p.Provision.Native
			records[id] = r
		}
	}
	legacy, workloads, budgets := make(map[string]actionRecord), make(map[string]actionRecord), make(map[string]actionRecord)
	for id, entry := range m.actions {
		if workloadHistoryAction(entry.Action) {
			workloads[id] = entry
		} else if entry.Action == "route_budgets" {
			budgets[id] = entry
		} else {
			legacy[id] = entry
		}
	}
	defaults, named := m.serviceAbortOutcomeSnapshots()
	return m.opts.planStore.updateMany(map[string]any{
		namedServiceAbortOutcomeNamespace + m.opts.planNamespace: named,
		serviceAbortOutcomeNamespace + m.opts.planNamespace:      defaults,
		rollbackOutcomeNamespace + m.opts.planNamespace:          rollbackOutcomeSnapshot{Version: 1, Items: m.rollbackOutcomes},
		promotionOutcomeNamespace + m.opts.planNamespace:         promotionOutcomeSnapshot{Version: 1, Items: m.promotionOutcomes},
		deploymentOutcomeNamespace + m.opts.planNamespace:        deploymentOutcomeSnapshot{Version: 1, Items: m.deploymentOutcomes},
		budgetOutcomeNamespace + m.opts.planNamespace:            budgetOutcomeSnapshot{Version: 1, Items: m.budgetOutcomes},
		"environment-plans/" + m.opts.planNamespace:              environments,
		environmentActionsNamespace + m.opts.planNamespace:       environmentActionSnapshot{Version: 1, Items: m.environmentActions},
		"plans/" + m.opts.planNamespace:                          records,
		"actions/" + m.opts.planNamespace:                        actionHistorySnapshot{Version: 1, Items: legacy},
		workloadActionsNamespace + m.opts.planNamespace:          actionHistorySnapshot{Version: 1, Items: workloads},
		budgetActionsNamespace + m.opts.planNamespace:            actionHistorySnapshot{Version: 1, Items: budgets},
	})
}

func temporaryPlan(p *releasePlan) bool {
	durableProvision := p.Provision != nil && p.Provision.Native != nil
	return p.LocalSource != nil || p.EnvChange != nil || p.Provision != nil && !durableProvision || p.Action == "local_deploy" || p.Action == "env_change" || strings.HasPrefix(p.Action, "provision_") && !durableProvision
}
