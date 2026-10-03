package state

import (
	"context"
	"fmt"
	"time"
)

var (
	_ AccountAbuseHoldStore = (*PgStore)(nil)
	_ AccountAbuseHoldStore = (*MemStore)(nil)
)

// SetAccountAbuseHold implements AccountAbuseHoldStore. An existing hold is
// kept as is, so the first reason and time stay on record.
func (s *PgStore) SetAccountAbuseHold(ctx context.Context, accountID, reason string, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update accounts
		   set abuse_hold_at = $3, abuse_hold_reason = $2
		 where id = $1 and abuse_hold_at is null`, accountID, reason, at.UTC())
	if err != nil {
		return false, fmt.Errorf("state: set account abuse hold %s: %w", accountID, mapErr(err))
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	if _, err := s.AccountByID(ctx, accountID); err != nil {
		return false, err
	}
	return false, nil
}

// ReleaseAccountAbuseHold implements AccountAbuseHoldStore.
func (s *PgStore) ReleaseAccountAbuseHold(ctx context.Context, accountID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update accounts
		   set abuse_hold_at = null, abuse_hold_reason = null
		 where id = $1 and abuse_hold_at is not null`, accountID)
	if err != nil {
		return false, fmt.Errorf("state: release account abuse hold %s: %w", accountID, mapErr(err))
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	if _, err := s.AccountByID(ctx, accountID); err != nil {
		return false, err
	}
	return false, nil
}

// SetAccountAbuseHold implements AccountAbuseHoldStore.
func (m *MemStore) SetAccountAbuseHold(_ context.Context, accountID, reason string, at time.Time) (bool, error) {
	if reason != AccountAbuseHoldEgressFanout && reason != AccountAbuseHoldEgressFlood && reason != AccountAbuseHoldOperator {
		return false, fmt.Errorf("state: set account abuse hold %s: invalid reason %q", accountID, reason)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return false, ErrNotFound
	}
	if a.AbuseHoldAt != nil {
		return false, nil
	}
	held := at.UTC()
	a.AbuseHoldAt, a.AbuseHoldReason = &held, reason
	m.storeRouteCheckAccountLocked(accountID, a)
	return true, nil
}

// ReleaseAccountAbuseHold implements AccountAbuseHoldStore.
func (m *MemStore) ReleaseAccountAbuseHold(_ context.Context, accountID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return false, ErrNotFound
	}
	if a.AbuseHoldAt == nil {
		return false, nil
	}
	a.AbuseHoldAt, a.AbuseHoldReason = nil, ""
	m.storeRouteCheckAccountLocked(accountID, a)
	return true, nil
}
