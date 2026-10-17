package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ FinancialBudgetNotificationStore = (*MemStore)(nil)

type financialBudgetNotificationKey struct {
	policyID    string
	periodStart time.Time
	threshold   int64
}

func (m *MemStore) ListFinancialBudgetNotifyPolicies(_ context.Context, limit int) ([]FinancialBudget, error) {
	if limit < 1 || limit > api.FinancialBudgetNotifyBatch {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialBudget{}
	for _, p := range m.financialBudgets {
		if p.DeletedAt == nil && p.Spec.Enabled && p.Spec.Mode == "monitored" && p.Spec.Action == "notify" {
			out = append(out, cloneFinancialBudget(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ClaimFinancialBudgetNotification(_ context.Context, n FinancialBudgetNotification) (bool, error) {
	if !validFinancialBudgetNotification(n) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.financialBudgets[n.PolicyID]; !ok || p.AccountID != n.AccountID {
		return false, ErrNotFound
	}
	key := financialBudgetNotificationKey{policyID: n.PolicyID, periodStart: n.PeriodStart.UTC(), threshold: n.ThresholdMillicents}
	if _, exists := m.financialBudgetNotifications[key]; exists {
		return false, nil
	}
	if m.financialBudgetNotifications == nil {
		m.financialBudgetNotifications = map[financialBudgetNotificationKey]FinancialBudgetNotification{}
	}
	n.PeriodStart = n.PeriodStart.UTC()
	n.Status, n.Attempts, n.LastError, n.FinishedAt = FinancialBudgetNotificationPending, 0, "", nil
	n.ClaimedAt = time.Now().UTC()
	m.financialBudgetNotifications[key] = n
	return true, nil
}

func (m *MemStore) ListPendingFinancialBudgetNotifications(_ context.Context, limit int) ([]FinancialBudgetNotification, error) {
	if limit < 1 || limit > api.FinancialBudgetNotifyBatch {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialBudgetNotification{}
	for _, n := range m.financialBudgetNotifications {
		if n.Status == FinancialBudgetNotificationPending {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.ClaimedAt.Equal(b.ClaimedAt) {
			return a.ClaimedAt.Before(b.ClaimedAt)
		}
		if a.PolicyID != b.PolicyID {
			return a.PolicyID < b.PolicyID
		}
		if !a.PeriodStart.Equal(b.PeriodStart) {
			return a.PeriodStart.Before(b.PeriodStart)
		}
		return a.ThresholdMillicents < b.ThresholdMillicents
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) RecordFinancialBudgetNotificationOutcome(_ context.Context, policyID string, periodStart time.Time, maxThreshold int64, outcome, lastError string) (int64, error) {
	if !validFinancialBudgetNotificationOutcome(policyID, periodStart, maxThreshold, outcome, lastError) {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var changed int64
	now := time.Now().UTC()
	for key, n := range m.financialBudgetNotifications {
		if key.policyID != policyID || !key.periodStart.Equal(periodStart.UTC()) || key.threshold > maxThreshold || n.Status != FinancialBudgetNotificationPending {
			continue
		}
		if outcome != FinancialBudgetNotificationCancelled {
			n.Attempts++
		}
		n.LastError = lastError
		if outcome != FinancialBudgetNotificationFailed {
			n.Status = outcome
			at := now
			n.FinishedAt = &at
		}
		m.financialBudgetNotifications[key] = n
		changed++
	}
	return changed, nil
}
