// adr: 20261024223000
package mcpmanagement

import (
	"context"
	"errors"
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
