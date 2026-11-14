package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type spendCapMemoryNotice struct {
	SpendCapNoticeDelivery
	nextAttempt time.Time
	delivered   bool
}

func (m *MemStore) ReserveBillingUsageIntent(_ context.Context, provider, account string, meter BillingMeter, hour time.Time, quantity int64) (int64, error) {
	if provider == "" || account == "" || (meter != BillingMeterCompute && meter != BillingMeterEgress) || hour.IsZero() || quantity < 0 {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.billingResourceAccountLocked(account); !ok {
		return 0, ErrNotFound
	}
	key := billingUsageDeliveryKey(provider, account, meter, hour.UTC().Truncate(time.Hour))
	if old, ok := m.billingUsageIntents[key]; ok {
		return old, nil
	}
	if m.billingUsageIntents == nil {
		m.billingUsageIntents = map[string]int64{}
	}
	m.billingUsageIntents[key] = quantity
	return quantity, nil
}

func (m *MemStore) ClaimSpendCapNotice(_ context.Context, now time.Time) (SpendCapNoticeDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidates []string
	for id, n := range m.spendCapNotices {
		if !n.delivered && !n.nextAttempt.After(now) {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return SpendCapNoticeDelivery{}, ErrNotFound
	}
	id := candidates[0]
	n := m.spendCapNotices[id]
	n.LeaseToken = uuid.NewString()
	n.nextAttempt = now.Add(api.FinancialBudgetAlertLease)
	m.spendCapNotices[id] = n
	return n.SpendCapNoticeDelivery, nil
}

func (m *MemStore) SettleSpendCapNotice(_ context.Context, id, token string, now time.Time, sent bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.spendCapNotices[id]
	if !ok || token == "" || n.LeaseToken != token || !now.Before(n.nextAttempt) || n.delivered {
		return ErrConflict
	}
	n.LeaseToken, n.delivered = "", sent
	n.nextAttempt = now.Add(api.FinancialBudgetAlertRetryBase)
	m.spendCapNotices[id] = n
	return nil
}

func (m *MemStore) BillingPlanActivationHead(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.planActivationHead, nil
}

func (m *MemStore) ListBillingPlanActivations(_ context.Context, subject string, start, through time.Time) ([]BillingPlanActivation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BillingPlanActivation
	for _, a := range m.planActivations[subject] {
		if !a.At.Before(start) && !a.At.After(through) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}

func (m *MemStore) FinancialBudgetJobs(_ context.Context, account string, limit int) ([]Job, error) {
	if limit < 1 || limit > api.FinancialAllocationMax+1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.AccountID == account && j.Status != "deleted" {
			out = append(out, Job{ID: j.ID, AccountID: j.AccountID, Name: j.Name, Status: j.Status, CreatedAt: j.CreatedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
