package mcpmanagement

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
)

// Flag, budget and reviewed plans are journaled in the connection's state
// namespace like release plans, so a hosted bridge restart (every release)
// keeps them until they expire (ADR-20261121140000). None holds a secret
// value: flag configurations, notify-only budget specs and experiment or
// schema requests are already visible to the account.
const memoryPlansNamespace = "memory-plans/"

type storedMemoryPlans struct {
	Version  int                           `json:"version"`
	Flags    map[string]storedFlagPlan     `json:"flags,omitempty"`
	Budgets  map[string]storedBudgetPlan   `json:"budgets,omitempty"`
	Reviewed map[string]storedReviewedPlan `json:"reviewed,omitempty"`
}

type storedFlagPlan struct {
	Plan      flagPlan       `json:"plan"`
	Config    flags.Config   `json:"config"`
	Receipt   map[string]any `json:"receipt,omitempty"`
	Attempted bool           `json:"attempted,omitempty"`
}

type storedBudgetPlan struct {
	Plan    budgetPlan      `json:"plan"`
	Receipt json.RawMessage `json:"receipt,omitempty"`
}

type storedReviewedPlan struct {
	Plan    reviewedPlan    `json:"plan"`
	Receipt json.RawMessage `json:"receipt,omitempty"`
}

// persistMemoryPlansLocked journals the unexpired plans. Callers hold m.mu
// and call it before an API write that depends on an attempt marker, as
// persistPlans is.
func (m *management) persistMemoryPlansLocked() error {
	if m.opts.planStore == nil {
		return nil
	}
	now := time.Now()
	out := storedMemoryPlans{Version: 1, Flags: map[string]storedFlagPlan{}, Budgets: map[string]storedBudgetPlan{}, Reviewed: map[string]storedReviewedPlan{}}
	for id, p := range m.flagPlans {
		if now.Before(p.ExpiresAt) {
			out.Flags[id] = storedFlagPlan{Plan: *p, Config: p.config, Receipt: p.receipt, Attempted: p.attempted}
		}
	}
	for id, p := range m.budgetPlans {
		if now.Before(p.ExpiresAt) {
			receipt, err := marshalReceipt(p.receipt)
			if err != nil {
				return err
			}
			out.Budgets[id] = storedBudgetPlan{Plan: *p, Receipt: receipt}
		}
	}
	for id, p := range m.reviewedPlans {
		if now.Before(p.ExpiresAt) {
			receipt, err := marshalReceipt(p.receipt)
			if err != nil {
				return err
			}
			out.Reviewed[id] = storedReviewedPlan{Plan: *p, Receipt: receipt}
		}
	}
	return m.opts.planStore.update(memoryPlansNamespace+m.opts.planNamespace, out)
}

func marshalReceipt(receipt any) (json.RawMessage, error) {
	if receipt == nil {
		return nil, nil
	}
	return json.Marshal(receipt)
}

// restoreMemoryPlans reloads unexpired plans. A plan bound to another
// account, or with a malformed identity, is dropped: the agent plans again.
func (m *management) restoreMemoryPlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	var saved storedMemoryPlans
	if err := m.opts.planStore.read(memoryPlansNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	now := time.Now()
	keep := func(id, planID, account string, expires time.Time) bool {
		return id == planID && len(id) == 64 && now.Before(expires) && (m.opts.accountID == "" || account == m.opts.accountID)
	}
	for id, s := range saved.Flags {
		if keep(id, s.Plan.ID, s.Plan.AccountID, s.Plan.ExpiresAt) && len(m.flagPlans) < maxFlagPlans {
			p := s.Plan
			p.config, p.receipt, p.attempted = s.Config, s.Receipt, s.Attempted
			m.flagPlans[id] = &p
		}
	}
	for id, s := range saved.Budgets {
		if keep(id, s.Plan.ID, s.Plan.AccountID, s.Plan.ExpiresAt) {
			p := s.Plan
			if len(s.Receipt) > 0 {
				p.receipt = s.Receipt
			}
			m.budgetPlans[id] = &p
		}
	}
	for id, s := range saved.Reviewed {
		if keep(id, s.Plan.ID, s.Plan.AccountID, s.Plan.ExpiresAt) {
			p := s.Plan
			if len(s.Receipt) > 0 {
				p.receipt = s.Receipt
			}
			m.reviewedPlans[id] = &p
		}
	}
	return nil
}

// liveMemoryPlans reports an unexpired journaled plan, which keeps a headless
// connection's state through OAuth cleanup.
func liveMemoryPlans(raw json.RawMessage, now time.Time) bool {
	var saved storedMemoryPlans
	if json.Unmarshal(raw, &saved) != nil {
		return false
	}
	for _, s := range saved.Flags {
		if now.Before(s.Plan.ExpiresAt) {
			return true
		}
	}
	for _, s := range saved.Budgets {
		if now.Before(s.Plan.ExpiresAt) {
			return true
		}
	}
	for _, s := range saved.Reviewed {
		if now.Before(s.Plan.ExpiresAt) {
			return true
		}
	}
	return false
}
