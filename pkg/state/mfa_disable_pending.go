package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Pending email-assisted MFA disables (F14-1, ADR-20261115100000). Kept out
// of Store; apid type-asserts MFADisablePendingStore.

// MFADisablePendingStore is implemented by MemStore and PgStore.
type MFADisablePendingStore interface {
	// CancelPendingMFADisable ends every pending email-assisted disable for
	// accountID and reports how many it ended.
	CancelPendingMFADisable(ctx context.Context, accountID string) (int, error)
	// PendingMFADisableRequestedAt returns when the earliest still-usable
	// pending disable was requested, or nil when there is none.
	PendingMFADisableRequestedAt(ctx context.Context, accountID string) (*time.Time, error)
}

var (
	_ MFADisablePendingStore = (*MemStore)(nil)
	_ MFADisablePendingStore = (*PgStore)(nil)
)

// mfaDisableUsableFor is how long an email-assisted disable request can still
// be confirmed after it was made: the cooldown plus the confirm window.
const mfaDisableUsableFor = api.MFADisableEmailCooldown + api.MFADisableEmailConfirmWindow

func (m *MemStore) CancelPendingMFADisable(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return 0, ErrNotFound
	}
	now := time.Now()
	n := 0
	for key, req := range m.mfaDisableRequests {
		if req.AccountID == accountID && req.ConsumedAt == nil {
			req.ConsumedAt = &now
			m.mfaDisableRequests[key] = req
			n++
		}
	}
	return n, nil
}

func (m *MemStore) PendingMFADisableRequestedAt(_ context.Context, accountID string) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-mfaDisableUsableFor)
	var earliest *time.Time
	for _, req := range m.mfaDisableRequests {
		if req.AccountID != accountID || req.ConsumedAt != nil || !req.RequestedAt.After(cutoff) {
			continue
		}
		if earliest == nil || req.RequestedAt.Before(*earliest) {
			at := req.RequestedAt
			earliest = &at
		}
	}
	return earliest, nil
}

func (s *PgStore) CancelPendingMFADisable(ctx context.Context, accountID string) (int, error) {
	id, err := parsePgUUID(accountID)
	if err != nil {
		return 0, ErrNotFound
	}
	n, err := sqlc.New().CancelPendingMFADisable(ctx, s.pool, id)
	if err != nil {
		return 0, fmt.Errorf("cancel pending mfa disable: %w", mapErr(err))
	}
	return int(n), nil
}

func (s *PgStore) PendingMFADisableRequestedAt(ctx context.Context, accountID string) (*time.Time, error) {
	id, err := parsePgUUID(accountID)
	if err != nil {
		return nil, ErrNotFound
	}
	at, err := sqlc.New().PendingMFADisableRequestedAt(ctx, s.pool, sqlc.PendingMFADisableRequestedAtParams{
		AccountID:     id,
		WindowSeconds: mfaDisableUsableFor.Seconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("read pending mfa disable: %w", mapErr(err))
	}
	return timestamptzPtr(at), nil
}

func timestamptzPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
