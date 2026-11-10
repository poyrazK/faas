package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AccountThrottleClearStore resets one account counter of
// account_mail_throttle. The shared TOTP attempt limit uses it: a correct
// code clears the counted attempts (ADR 20261110120000).
type AccountThrottleClearStore interface {
	ClearAccountMailSlot(ctx context.Context, accountID, kind string) error
}

var (
	_ AccountThrottleClearStore = (*MemStore)(nil)
	_ AccountThrottleClearStore = (*PgStore)(nil)
)

func (m *MemStore) ClearAccountMailSlot(_ context.Context, accountID, kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accountFlow.mailSlots, accountID+"\x00"+kind)
	return nil
}

func (s *PgStore) ClearAccountMailSlot(ctx context.Context, accountID, kind string) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return ErrNotFound
	}
	return mapErr(sqlc.New().ClearAccountMailSlot(ctx, s.pool, sqlc.ClearAccountMailSlotParams{AccountID: account, Kind: kind}))
}
