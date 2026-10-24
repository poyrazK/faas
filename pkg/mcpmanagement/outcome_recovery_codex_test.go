// adr: 20261024223000
package mcpmanagement

import (
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
