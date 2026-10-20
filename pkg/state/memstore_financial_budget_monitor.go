package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialBudgetMonitorStore = (*MemStore)(nil)

type financialBudgetMemoryEvaluation struct {
	revision int64
	value    financial.BudgetEvaluation
}

func (m *MemStore) RecordFinancialBudgetEvaluation(_ context.Context, account, id string, revision int64, e financial.BudgetEvaluation) error {
	if !validFinancialBudgetMutation(account, id, "meterd", revision) || !validFinancialBudgetEvaluation(e) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.financialBudgets[id]
	if !ok || p.AccountID != account {
		return ErrNotFound
	}
	if p.DeletedAt != nil || p.Revision != revision || !p.Spec.Enabled || !financial.Monitored(p.Spec) {
		return ErrConflict
	}
	old := m.financialBudgetEvaluations[id]
	if old.revision == revision && (old.value.EvaluatedAt.After(e.EvaluatedAt) || old.value.PeriodStart.Equal(e.PeriodStart) && old.value.EvidenceThroughID > e.EvidenceThroughID && e.KnownMillicents != nil) {
		return ErrConflict
	}
	if m.financialBudgetEvaluations == nil {
		m.financialBudgetEvaluations = map[string]financialBudgetMemoryEvaluation{}
		m.financialBudgetAlerts = map[string]FinancialBudgetAlertDelivery{}
	}
	m.financialBudgetEvaluations[id] = financialBudgetMemoryEvaluation{revision, cloneFinancialBudgetEvaluation(e)}
	m.recordFinancialBudgetStopLocked(p, e)
	for _, alert := range financialBudgetAlerts(p, e) {
		if _, exists := m.financialBudgetAlerts[alert.ID]; !exists {
			m.financialBudgetAlerts[alert.ID] = alert
		}
	}
	return nil
}

func (m *MemStore) FinancialBudgetRuntime(_ context.Context, account, id string, revision int64, month time.Time) (financial.BudgetRuntime, error) {
	if !validFinancialBudgetMutation(account, id, "reader", revision) || month.IsZero() || month.UTC().Day() != 1 || !month.Equal(month.UTC().Truncate(24*time.Hour)) {
		return financial.BudgetRuntime{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.financialBudgets[id]
	if !ok || p.AccountID != account {
		return financial.BudgetRuntime{}, ErrNotFound
	}
	if p.Revision != revision {
		return financial.BudgetRuntime{}, ErrConflict
	}
	out := financial.BudgetRuntime{Alerts: []financial.BudgetAlert{}}
	if e, ok := m.financialBudgetEvaluations[id]; ok && e.revision == p.Revision && e.value.PeriodStart.Equal(month) {
		value := cloneFinancialBudgetEvaluation(e.value)
		out.Evaluation = &value
	}
	for _, a := range m.financialBudgetAlerts {
		if a.AccountID == account && a.PolicyID == id && a.PolicyRevision == p.Revision && a.Evaluation.PeriodStart.Equal(month) {
			out.Alerts = append(out.Alerts, cloneFinancialBudgetAlert(a).BudgetAlert)
		}
	}
	sort.Slice(out.Alerts, func(i, j int) bool { return out.Alerts[i].ThresholdMillicents < out.Alerts[j].ThresholdMillicents })
	return out, nil
}

func (m *MemStore) ClaimFinancialBudgetAlert(_ context.Context, now time.Time) (FinancialBudgetAlertDelivery, error) {
	if now.IsZero() {
		return FinancialBudgetAlertDelivery{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var next FinancialBudgetAlertDelivery
	for _, a := range m.financialBudgetAlerts {
		p := m.financialBudgets[a.PolicyID]
		if p.DeletedAt != nil || p.Revision != a.PolicyRevision || !p.Spec.Enabled || !financial.Monitored(p.Spec) || a.State == "delivered" || a.NextAttemptAt.After(now) {
			continue
		}
		if next.ID == "" || a.NextAttemptAt.Before(next.NextAttemptAt) || a.NextAttemptAt.Equal(next.NextAttemptAt) && a.ID < next.ID {
			next = a
		}
	}
	if next.ID == "" {
		return next, ErrNotFound
	}
	next.State, next.LeaseToken = "sending", uuid.NewString()
	next.Attempts = min(next.Attempts+1, api.FinancialBudgetAlertAttemptsMax)
	next.NextAttemptAt = now.UTC().Add(api.FinancialBudgetAlertLease)
	m.financialBudgetAlerts[next.ID] = next
	return cloneFinancialBudgetAlert(next), nil
}

func (m *MemStore) SettleFinancialBudgetAlert(_ context.Context, id, token string, now time.Time, delivered bool) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil || now.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.financialBudgetAlerts[id]
	if !ok || a.State != "sending" || a.LeaseToken != token || !now.Before(a.NextAttemptAt) {
		return ErrConflict
	}
	a.LeaseToken = ""
	if delivered {
		at := now.UTC()
		a.State, a.DeliveredAt, a.LastError = "delivered", &at, ""
		a.NextAttemptAt = at
	} else {
		a.State, a.LastError = "retrying", "mail_delivery_failed"
		a.NextAttemptAt = now.UTC().Add(financialBudgetAlertRetry(a.Attempts))
	}
	m.financialBudgetAlerts[id] = a
	return nil
}
