package state

import (
	"context"
	"time"
)

func (m *MemStore) PutSpendCapHold(_ context.Context, hold SpendCapHold) error {
	if !validSpendCapHold(hold) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[hold.AccountID]; !ok {
		return ErrNotFound
	}
	if m.spendCapHolds == nil {
		m.spendCapHolds = make(map[string]SpendCapHold)
	}
	hold.Month = hold.Month.UTC()
	hold.ReachedAt = time.Now().UTC()
	if prior, ok := m.spendCapHolds[hold.AccountID]; ok && prior.Month.Equal(hold.Month) && prior.CapCents == hold.CapCents {
		hold.ReachedAt = prior.ReachedAt
	}
	m.spendCapHolds[hold.AccountID] = hold
	return nil
}

func (m *MemStore) ClearSpendCapHold(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.spendCapHolds, accountID)
	return nil
}

func (m *MemStore) GetSpendCapHold(_ context.Context, accountID string) (SpendCapHold, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hold, ok := m.spendCapHolds[accountID]
	return hold, ok, nil
}
