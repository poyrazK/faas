// adr: 20261024223000
package mcpmanagement

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Test control seeds bridge-local ambiguous history and a synthetic native GET
// receipt. It never calls a mutation endpoint or claims a real native commit.
func seedCodexOutcomeHistory(remote *remoteServer) (api.EnvironmentRouteBudgetReceipt, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	const app = "budget-history"
	id := strings.Repeat("e", 64)
	for _, m := range managers {
		m.mu.Lock()
		entry, exists := m.actions[id]
		if !exists {
			for _, p := range m.plans {
				if p.Action != "route_budgets" || p.App != app {
					continue
				}
				seed := *p
				seed.ID, seed.ExpiresAt, seed.attempted = id, time.Now().Add(-time.Minute), true
				seed.budgetResult = nil
				m.plans[id] = &seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.EnvironmentRouteBudgetReceipt{}, err
				}
				entry, exists = m.actions[id]
				break
			}
		}
		if exists {
			lookup := m.budgetOutcomes[id]
			receipt := api.EnvironmentRouteBudgetReceipt{RequestID: lookup.RequestID, RequestHash: lookup.RequestHash, AppID: entry.AppID, Scope: entry.Scope, Revision: entry.Budget.ExpectedRevision + 1, AppRevision: entry.Budget.ExpectedAppRevision, BudgetSource: "app_policy"}
			m.mu.Unlock()
			return receipt, nil
		}
		m.mu.Unlock()
	}
	return api.EnvironmentRouteBudgetReceipt{}, errors.New("missing budget fixture plan")
}

// Seed synthetic source-deployment acceptance without invoking a native write.
func seedCodexDeploymentOutcome(remote *remoteServer) (api.DeploymentSubmissionReceipt, api.DeploymentResponse, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("d", 64)
	for _, m := range managers {
		m.mu.Lock()
		for _, p := range m.plans {
			if p.Action != "deploy" || p.Source == nil || p.App != "alpha" || p.ID == id {
				continue
			}
			if _, found := m.actions[id]; !found {
				seed := *p
				seed.ID, seed.ExpiresAt = id, time.Now().Add(-time.Minute)
				if _, err := m.prepareDeploymentSubmission(context.Background(), &seed); err != nil {
					m.mu.Unlock()
					return api.DeploymentSubmissionReceipt{}, api.DeploymentResponse{}, err
				}
				seed.attempted, seed.result = true, nil
				m.plans[id] = &seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.DeploymentSubmissionReceipt{}, api.DeploymentResponse{}, err
				}
			}
			entry, ref := m.actions[id], m.deploymentOutcomes[id]
			d := api.DeploymentResponse{ID: "accepted-source-deployment", AppID: entry.AppID, Scope: entry.Scope, Kind: "github", CommitSHA: p.Source.CommitSHA, BuildID: "accepted-source-build", Status: "failed", TrafficPercent: 0}
			receipt := api.DeploymentSubmissionReceipt{AttemptID: ref.AttemptID, AppID: d.AppID, DeploymentID: d.ID, AcceptedAt: entry.RecordedAt}
			m.mu.Unlock()
			return receipt, d, nil
		}
		m.mu.Unlock()
	}
	return api.DeploymentSubmissionReceipt{}, api.DeploymentResponse{}, errors.New("missing source fixture plan")
}

// Synthetic expired promotion receipt: no native mutation endpoint is invoked.
func seedCodexPromotionOutcome(remote *remoteServer) (api.DeploymentPromotionReceipt, api.DeploymentResponse, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("c", 64)
	for _, m := range managers {
		m.mu.Lock()
		for _, p := range m.plans {
			if p.Action != "deploy" || p.Source == nil || p.App != "alpha" || p.ID == id {
				continue
			}
			d := api.DeploymentResponse{ID: "promoted-source-deployment", AppID: p.AppID, Scope: p.Scope, Kind: "github", CommitSHA: p.Source.CommitSHA, BuildID: "promoted-source-build", Status: "superseded", TrafficPercent: 0}
			if _, found := m.actions[id]; !found {
				seed := *p
				seed.ID, seed.ExpiresAt = id, time.Now().Add(-time.Minute)
				seed.attempted, seed.result = true, &d
				if _, err := m.preparePromotionOutcome(context.Background(), &seed); err != nil {
					m.mu.Unlock()
					return api.DeploymentPromotionReceipt{}, api.DeploymentResponse{}, err
				}
				seed.promotionAttempted = true
				seed.promoted = nil
				m.plans[id] = &seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.DeploymentPromotionReceipt{}, api.DeploymentResponse{}, err
				}
			}
			entry, ref := m.actions[id], m.promotionOutcomes[id]
			receipt := api.DeploymentPromotionReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, AppID: entry.AppID, DeploymentID: ref.CandidateID, ExpectedServingDeploymentID: ref.ExpectedServingID, Scope: entry.Scope, ProjectID: entry.ProjectID, EnvironmentID: entry.EnvironmentID, AcceptedAt: entry.RecordedAt}
			m.mu.Unlock()
			return receipt, d, nil
		}
		m.mu.Unlock()
	}
	return api.DeploymentPromotionReceipt{}, api.DeploymentResponse{}, errors.New("missing promotion fixture plan")
}

// Seed historical native metadata only; this fixture performs no native writes.
func seedCodexRollbackOutcome(remote *remoteServer, serviceMode ...bool) (api.RollbackOperation, error) {
	service := len(serviceMode) > 0 && serviceMode[0]
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("b", 64)
	if service {
		id = strings.Repeat("3", 64)
	}
	for _, m := range managers {
		m.mu.Lock()
		if _, found := m.actions[id]; !found {
			for _, p := range m.plans {
				if p.Action != "deploy" || p.App != "alpha" {
					continue
				}
				seed := *p
				seed.ID, seed.Action, seed.RolloutMode = id, "rollback", api.ExecutionModeRequest
				if service {
					seed.RolloutMode = api.ExecutionModeService
				}
				seed.TargetDeploymentID, seed.ExpectedCurrentID = "rollback-target", "rollback-serving"
				seed.Reason, seed.Source, seed.attempted, seed.result = "PRIVATE_ROLLBACK_REASON", nil, false, nil
				seed.ExpiresAt = time.Now().Add(-time.Minute)
				if _, err := m.prepareRollbackOutcome(context.Background(), &seed); err != nil {
					m.mu.Unlock()
					return api.RollbackOperation{}, err
				}
				seed.attempted = true
				m.plans[id] = &seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.RollbackOperation{}, err
				}
				break
			}
		}
		entry, found := m.actions[id]
		ref := m.rollbackOutcomes[id]
		if service {
			ref = m.serviceRollbackOutcomes[id]
		}
		if found && validRollbackOutcomeLookup(ref) {
			operation := api.RollbackOperation{ID: uuid.MustParse(ref.AttemptID).String(), AppID: entry.AppID, Scope: entry.Scope, Service: service, TargetDeploymentID: ref.TargetID, CurrentDeploymentID: ref.CurrentID, Reason: "PRIVATE_ROLLBACK_REASON", Status: "failed", Code: "later_failure", CreatedAt: entry.RecordedAt, UpdatedAt: time.Now().UTC()}
			m.mu.Unlock()
			return operation, nil
		}
		m.mu.Unlock()
	}
	return api.RollbackOperation{}, errors.New("missing request rollback fixture plan")
}

// Synthetic service-abort acceptance survives expiry; no native write is made.
func seedCodexServiceAbortOutcome(remote *remoteServer, named bool) (api.ServiceAbortReceipt, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("1", 64)
	if named {
		id = strings.Repeat("2", 64)
	}
	for _, m := range managers {
		m.mu.Lock()
		if _, found := m.actions[id]; !found {
			for _, p := range m.plans {
				if p.Action != "deploy" || p.App != "alpha" {
					continue
				}
				seed := *p
				seed.ID, seed.Action, seed.RolloutMode = id, "service_abort", api.ExecutionModeService
				seed.TargetDeploymentID, seed.PredecessorDeploymentID = "service-abort-target", "service-abort-serving"
				seed.Reason, seed.Source, seed.attempted, seed.result = "PRIVATE_SERVICE_ABORT_REASON", nil, false, nil
				seed.ExpiresAt = time.Now().Add(-time.Minute)
				if named {
					grant, ok := m.opts.Environments.Named["shop/staging"]
					if !ok {
						continue
					}
					seed.Project, seed.Scope = "shop", "staging"
					seed.ProjectID, seed.EnvironmentID = grant.ProjectID, grant.EnvironmentID
					seed.TargetDeploymentID, seed.PredecessorDeploymentID = "named-service-abort-target", "named-service-abort-serving"
					if seed.review != nil {
						review := *seed.review
						review.Scope, review.ProjectID, review.EnvironmentID = seed.Scope, seed.ProjectID, seed.EnvironmentID
						seed.review = &review
					}
				}
				if _, err := m.prepareServiceAbortOutcome(context.Background(), &seed); err != nil {
					m.mu.Unlock()
					return api.ServiceAbortReceipt{}, err
				}
				seed.attempted = true
				m.plans[id] = &seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.ServiceAbortReceipt{}, err
				}
				break
			}
		}
		entry, found := m.actions[id]
		ref := m.serviceAbortOutcomes[id]
		if found && validServiceAbortOutcomeLookup(ref) {
			receipt := api.ServiceAbortReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, AppID: entry.AppID, Scope: entry.Scope, ProjectID: entry.ProjectID, EnvironmentID: entry.EnvironmentID, DeploymentID: ref.TargetID, PredecessorDeploymentID: ref.PredecessorID, RequestID: "d5c0a595-f6d7-415e-a9d8-d6634052f03a", AcceptedAt: entry.RecordedAt}
			m.mu.Unlock()
			return receipt, nil
		}
		m.mu.Unlock()
	}
	return api.ServiceAbortReceipt{}, errors.New("missing service abort fixture plan")
}

func seedCodexEnvironmentRollbackOutcome(remote *remoteServer) (api.EnvironmentRollbackReceipt, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("4", 64)
	for _, m := range managers {
		m.mu.Lock()
		if _, found := m.environmentActions[id]; !found {
			for _, p := range m.environmentPlans {
				if p.Action != "environment_promotion" {
					continue
				}
				seed := &environmentPlan{ID: id, Action: "environment_rollback", AccountID: p.AccountID, APIBase: p.APIBase, Project: p.Project, ProjectID: p.ProjectID, FromEnvironment: p.FromEnvironment, FromID: p.FromID, Environment: p.Environment, EnvironmentID: p.EnvironmentID, PromotionID: "promotion-one", PromotionHash: p.PromotionHash, Reason: "PRIVATE_ENV_ROLLBACK_REASON", ExpiresAt: time.Now().Add(-time.Minute)}
				if _, err := m.prepareEnvironmentRollbackOutcome(context.Background(), seed); err != nil {
					m.mu.Unlock()
					return api.EnvironmentRollbackReceipt{}, err
				}
				seed.attempted = true
				m.environmentPlans[id] = seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.EnvironmentRollbackReceipt{}, err
				}
				break
			}
		}
		entry, found := m.environmentActions[id]
		ref := m.environmentRollbackOutcomes[id]
		if found && validEnvironmentRollbackLookup(ref) {
			receipt := api.EnvironmentRollbackReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, PromotionHash: ref.PromotionHash, PromotionID: entry.PromotionID, ProjectID: entry.ProjectID, FromEnvironmentID: entry.FromID, EnvironmentID: entry.EnvironmentID, AcceptedAt: entry.RecordedAt}
			m.mu.Unlock()
			return receipt, nil
		}
		m.mu.Unlock()
	}
	return api.EnvironmentRollbackReceipt{}, errors.New("missing environment promotion fixture plan")
}

func seedCodexEnvironmentPromotionOutcome(remote *remoteServer) (api.EnvironmentPromotionReceipt, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("6", 64)
	for _, m := range managers {
		m.mu.Lock()
		if _, found := m.environmentActions[id]; !found {
			for _, p := range m.environmentPlans {
				if p.Action != "environment_promotion" {
					continue
				}
				seed := &environmentPlan{ID: id, Action: "environment_promotion", AccountID: p.AccountID, APIBase: p.APIBase, Project: p.Project, ProjectID: p.ProjectID, FromEnvironment: p.FromEnvironment, FromID: p.FromID, Environment: p.Environment, EnvironmentID: p.EnvironmentID, PromotionHash: p.PromotionHash, Reason: "PRIVATE_ENV_ROLLBACK_REASON", ExpiresAt: time.Now().Add(-time.Minute)}
				if _, err := m.prepareEnvironmentPromotionOutcome(context.Background(), seed); err != nil {
					m.mu.Unlock()
					return api.EnvironmentPromotionReceipt{}, err
				}
				seed.attempted = true
				m.environmentPlans[id] = seed
				if err := m.persistPlans(); err != nil {
					m.mu.Unlock()
					return api.EnvironmentPromotionReceipt{}, err
				}
				break
			}
		}
		entry, found := m.environmentActions[id]
		ref := m.environmentPromotionOutcomes[id]
		if found && validEnvironmentRollbackLookup(ref) {
			receipt := api.EnvironmentPromotionReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, PromotionHash: ref.PromotionHash, PromotionID: "3e9f323a-ade6-442b-8444-c91da107fe44", ProjectID: entry.ProjectID, FromEnvironmentID: entry.FromID, EnvironmentID: entry.EnvironmentID, AcceptedAt: entry.RecordedAt}
			m.mu.Unlock()
			return receipt, nil
		}
		m.mu.Unlock()
	}
	return api.EnvironmentPromotionReceipt{}, errors.New("missing environment promotion fixture plan")
}
