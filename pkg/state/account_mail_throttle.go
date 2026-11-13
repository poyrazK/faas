package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Per-account mail counters shared by every apid (ADR 20261108160000).
// Kept out of Store; apid type-asserts AccountMailThrottleStore.

// MailSlotLimit bounds one kind of mail to one account: at most
// MaxPerWindow sends per fixed Window, and none sooner than MinInterval
// after the last.
type MailSlotLimit struct {
	Window       time.Duration
	MaxPerWindow int
	MinInterval  time.Duration
}

// AccountMailThrottleStore is implemented by MemStore and PgStore.
type AccountMailThrottleStore interface {
	// TakeAccountMailSlot counts one mail of kind to the account and
	// reports true, or reports false when limit refuses it. ErrNotFound
	// when the account does not exist.
	TakeAccountMailSlot(ctx context.Context, accountID, kind string, limit MailSlotLimit) (bool, error)
}

var (
	_ AccountMailThrottleStore = (*MemStore)(nil)
	_ AccountMailThrottleStore = (*PgStore)(nil)
)

type memMailSlot struct {
	windowStarted, last time.Time
	sent                int
}

func (m *MemStore) TakeAccountMailSlot(_ context.Context, accountID, kind string, limit MailSlotLimit) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return false, ErrNotFound
	}
	if m.accountFlow.mailSlots == nil {
		m.accountFlow.mailSlots = map[string]*memMailSlot{}
	}
	now := time.Now()
	key := accountID + "\x00" + kind
	slot, ok := m.accountFlow.mailSlots[key]
	if !ok {
		m.accountFlow.mailSlots[key] = &memMailSlot{windowStarted: now, last: now, sent: 1}
		return true, nil
	}
	expired := !slot.windowStarted.After(now.Add(-limit.Window))
	if slot.last.After(now.Add(-limit.MinInterval)) || (!expired && slot.sent >= limit.MaxPerWindow) {
		return false, nil
	}
	if expired {
		slot.windowStarted, slot.sent = now, 0
	}
	slot.sent++
	slot.last = now
	return true, nil
}

func (s *PgStore) TakeAccountMailSlot(ctx context.Context, accountID, kind string, limit MailSlotLimit) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, ErrNotFound
	}
	_, err = sqlc.New().TakeAccountMailSlot(ctx, s.pool, sqlc.TakeAccountMailSlotParams{
		AccountID:          account,
		Kind:               kind,
		WindowSeconds:      limit.Window.Seconds(),
		MinIntervalSeconds: limit.MinInterval.Seconds(),
		MaxPerWindow:       int32(min(max(limit.MaxPerWindow, 0), 1000)), //nolint:gosec // clamped
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	return true, nil
}

// AgeAccountMailSlotsForTest moves every mail counter d into the past, as
// if the next send came d later; a negative d forgets them all.
func (m *MemStore) AgeAccountMailSlotsForTest(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d < 0 {
		m.accountFlow.mailSlots = nil
		return
	}
	for _, slot := range m.accountFlow.mailSlots {
		slot.windowStarted = slot.windowStarted.Add(-d)
		slot.last = slot.last.Add(-d)
	}
}
