package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialBudgetProjectionStore = (*MemStore)(nil)

// ClaimFinancialBudgetProjection mirrors PgStore and ClaimFinancialBudgetAlert.
func (m *MemStore) ClaimFinancialBudgetProjection(_ context.Context, now time.Time) (FinancialBudgetAlertDelivery, error) {
	if now.IsZero() {
		return FinancialBudgetAlertDelivery{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var next FinancialBudgetAlertDelivery
	for _, a := range m.financialBudgetProjections {
		p := m.financialBudgets[a.PolicyID]
		if p.DeletedAt != nil || p.Revision != a.PolicyRevision || !p.Spec.Enabled || !financial.Monitored(p.Spec) || a.State == "delivered" || a.NextAttemptAt.After(now) {
			continue
		}
		if next.ID == "" || a.NextAttemptAt.Before(next.NextAttemptAt) || a.NextAttemptAt.Equal(next.NextAttemptAt) && a.ID < next.ID {
			a.Spec = p.Spec
			next = a
		}
	}
	if next.ID == "" {
		return next, ErrNotFound
	}
	next.State, next.LeaseToken = "sending", uuid.NewString()
	next.Attempts = min(next.Attempts+1, api.FinancialBudgetAlertAttemptsMax)
	next.NextAttemptAt = now.UTC().Add(api.FinancialBudgetAlertLease)
	m.financialBudgetProjections[next.ID] = next
	return cloneFinancialBudgetAlert(next), nil
}

// SettleFinancialBudgetProjection mirrors PgStore and SettleFinancialBudgetAlert.
func (m *MemStore) SettleFinancialBudgetProjection(_ context.Context, id, token string, now time.Time, delivered bool) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil || now.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.financialBudgetProjections[id]
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
	m.financialBudgetProjections[id] = a
	return nil
}
