package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialBudgetStopStore = (*MemStore)(nil)

func (m *MemStore) recordFinancialBudgetStopLocked(p FinancialBudget, e financial.BudgetEvaluation) {
	s, crossed := financialBudgetStop(p, e)
	if !crossed {
		return
	}
	if m.financialBudgetStops == nil {
		m.financialBudgetStops = map[string]FinancialBudgetStop{}
	}
	for _, old := range m.financialBudgetStops {
		if old.PolicyID == p.ID && old.Revision == p.Revision && (old.ResumeRule == "manual" || old.PeriodStart.Equal(e.PeriodStart)) {
			return
		}
	}
	m.financialBudgetStops[s.ID] = s
}

func (m *MemStore) ActiveFinancialBudgetStops(_ context.Context, account string, now time.Time) ([]FinancialBudgetStop, error) {
	if uuid.Validate(account) != nil || now.IsZero() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialBudgetStop{}
	for _, s := range m.financialBudgetStops {
		if s.AccountID == account && financialBudgetStopActive(s, m.financialBudgets[s.PolicyID], now) {
			s.Targets = nil // Admission reads decisions without target-history fanout.
			out = append(out, cloneFinancialBudgetStop(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PolicyID < out[j].PolicyID })
	return out, nil
}

func (m *MemStore) FinancialBudgetStopTargets(_ context.Context, account, id string) ([]financial.BudgetStopTarget, error) {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, exists := m.financialBudgetStops[id]
	if !exists || s.AccountID != account {
		return []financial.BudgetStopTarget{}, nil
	}
	return cloneFinancialBudgetStop(s).Targets, nil
}

func (m *MemStore) RecordFinancialBudgetStopTarget(_ context.Context, account, id string, target financial.BudgetStopTarget) error {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil || !validFinancialBudgetStopTarget(target) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, exists := m.financialBudgetStops[id]
	if !exists || s.AccountID != account {
		return ErrNotFound
	}
	if !financialBudgetStopActive(s, m.financialBudgets[s.PolicyID], target.ObservedAt) || !s.Matches(m.apps[target.AppID], target.EnvironmentID) {
		return ErrConflict
	}
	if s.Spec.Scope.Kind == "environment" {
		env := m.projectEnvironments[s.Spec.Scope.ID]
		if env.AccountID != account || env.ProjectID != m.apps[target.AppID].ProjectID {
			return ErrConflict
		}
	}
	for i, old := range s.Targets {
		if old.AppID == target.AppID && old.EnvironmentID == target.EnvironmentID {
			if old.ObservedAt.After(target.ObservedAt) {
				return ErrConflict
			}
			s.Targets[i] = target
			m.financialBudgetStops[id] = s
			return nil
		}
	}
	s.Targets = append(s.Targets, target)
	sort.Slice(s.Targets, func(i, j int) bool {
		return s.Targets[i].AppID+s.Targets[i].EnvironmentID < s.Targets[j].AppID+s.Targets[j].EnvironmentID
	})
	m.financialBudgetStops[id] = s
	return nil
}
