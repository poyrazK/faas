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
func seedCodexRollbackOutcome(remote *remoteServer) (api.RollbackOperation, error) {
	remote.mu.Lock()
	var managers []*management
	for _, grant := range remote.grants {
		if !grant.revoked && grant.management != nil {
			managers = append(managers, grant.management)
		}
	}
	remote.mu.Unlock()
	id := strings.Repeat("b", 64)
	for _, m := range managers {
		m.mu.Lock()
		if _, found := m.actions[id]; !found {
			for _, p := range m.plans {
				if p.Action != "deploy" || p.App != "alpha" {
					continue
				}
				seed := *p
				seed.ID, seed.Action, seed.RolloutMode = id, "rollback", api.ExecutionModeRequest
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
		if found && validRollbackOutcomeLookup(ref) {
			operation := api.RollbackOperation{ID: uuid.MustParse(ref.AttemptID).String(), AppID: entry.AppID, Scope: entry.Scope, TargetDeploymentID: ref.TargetID, CurrentDeploymentID: ref.CurrentID, Reason: "PRIVATE_ROLLBACK_REASON", Status: "failed", Code: "later_failure", CreatedAt: entry.RecordedAt, UpdatedAt: time.Now().UTC()}
			m.mu.Unlock()
			return operation, nil
		}
		m.mu.Unlock()
	}
	return api.RollbackOperation{}, errors.New("missing request rollback fixture plan")
}
