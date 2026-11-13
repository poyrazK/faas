package mcpmanagement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A separate encrypted namespace lets N-1 bridges preserve these read-only
// references without learning a new action format or retaining write payloads.
const budgetOutcomeNamespace = "budget-outcome-lookups/"

type budgetOutcomeLookup struct {
	RequestID   string
	RequestHash string
	Binding     string
}

type budgetOutcomeSnapshot struct {
	Version int
	Items   map[string]budgetOutcomeLookup
}

func budgetOutcomeBinding(e actionRecord) string {
	// Apply/receipt state may advance; the original target, intent revisions and
	// retention are immutable. No mutation body is stored in this reference.
	body, _ := json.Marshal(struct {
		PlanID, Account, API, App, AppID, Scope, Project, ProjectID, EnvironmentID string
		Expires, Retained                                                          time.Time
		Budget                                                                     *actionBudget
	}{e.PlanID, e.AccountID, e.APIBase, e.App, e.AppID, e.Scope, e.Project, e.ProjectID, e.EnvironmentID, e.PlanExpiresAt, e.RetainedUntil, e.Budget})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func validOutcomeHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func validBudgetOutcomeLookup(lookup budgetOutcomeLookup) bool {
	id, err := uuid.Parse(lookup.RequestID)
	return err == nil && id != uuid.Nil && id.String() == lookup.RequestID && validOutcomeHash(lookup.RequestHash) && validOutcomeHash(lookup.Binding)
}

func (m *management) restoreBudgetOutcomeLookups() error {
	var saved budgetOutcomeSnapshot
	if err := m.opts.planStore.read(budgetOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid budget outcome lookup snapshot")
	}
	m.budgetOutcomes = make(map[string]budgetOutcomeLookup)
	for id, lookup := range saved.Items {
		if !validActionID(id) || !validBudgetOutcomeLookup(lookup) {
			return errors.New("invalid budget outcome lookup")
		}
		entry, ok := m.actions[id]
		// Older bridges can prune history while preserving unknown namespaces.
		if ok && time.Now().Before(entry.RetainedUntil) && (entry.Action != "route_budgets" || budgetOutcomeBinding(entry) != lookup.Binding) {
			return errors.New("budget outcome lookup binding mismatch")
		}
		m.budgetOutcomes[id] = lookup
	}
	return nil
}

func (m *management) syncBudgetOutcomeLookups(now time.Time) error {
	if m.budgetOutcomes == nil {
		m.budgetOutcomes = make(map[string]budgetOutcomeLookup)
	}
	for id, p := range m.plans {
		entry, ok := m.actions[id]
		if !ok || p.Action != "route_budgets" || !p.attempted || !now.Before(entry.RetainedUntil) {
			continue
		}
		if p.Budgets == nil {
			return invalid("invalid_plan", "missing original budget request")
		}
		hash, err := api.EnvironmentRouteBudgetRequestHash(p.Scope, p.Budgets.Proposed)
		if err != nil {
			return err
		}
		lookup := budgetOutcomeLookup{RequestID: p.Budgets.Proposed.RequestID, RequestHash: hash, Binding: budgetOutcomeBinding(entry)}
		if !validBudgetOutcomeLookup(lookup) {
			return invalid("invalid_plan", "invalid budget outcome reference")
		}
		m.budgetOutcomes[id] = lookup
	}
	for id, lookup := range m.budgetOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.budgetOutcomes, id)
			continue
		}
		if entry.Action != "route_budgets" || entry.Apply.State == "not_attempted" || lookup.Binding != budgetOutcomeBinding(entry) {
			return invalid("invalid_plan", "budget outcome binding changed")
		}
	}
	return nil
}
