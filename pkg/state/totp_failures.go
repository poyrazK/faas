package state

// Shared per-account TOTP failure window (ADR-1237).
//
// apid limits wrong TOTP codes per account (10 per 15 minutes) so someone
// holding the password cannot walk the code space from many addresses. With
// apid on several control nodes that window must live in the database:
// per-replica windows would grant each replica its own budget of guesses.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TOTPFailureWindow is the state of one account's window, timed by the
// database clock: Failures counted failures inside it, Oldest is the oldest
// of them (CheckedAt when there are none).
type TOTPFailureWindow struct {
	Failures  int
	Oldest    time.Time
	CheckedAt time.Time
}

// TOTPFailureStore is the narrow surface apid's TOTP guard needs.
type TOTPFailureStore interface {
	TOTPFailures(ctx context.Context, accountID string, window time.Duration) (TOTPFailureWindow, error)
	RecordTOTPFailure(ctx context.Context, accountID string, window time.Duration) error
	ResetTOTPFailures(ctx context.Context, accountID string) error
}

func totpAccountUUID(accountID string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(accountID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("state: TOTP failure window: account id %q: %w", accountID, ErrInvalidArgument)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func totpWindowSeconds(window time.Duration) (int64, error) {
	if window < time.Second {
		return 0, errors.New("state: TOTP failure window must be at least one second")
	}
	return int64(window / time.Second), nil
}

func (s *PgStore) TOTPFailures(ctx context.Context, accountID string, window time.Duration) (TOTPFailureWindow, error) {
	id, err := totpAccountUUID(accountID)
	if err != nil {
		return TOTPFailureWindow{}, err
	}
	secs, err := totpWindowSeconds(window)
	if err != nil {
		return TOTPFailureWindow{}, err
	}
	row, err := sqlc.New().TOTPFailureWindow(ctx, s.pool, sqlc.TOTPFailureWindowParams{AccountID: id, WindowSeconds: secs})
	if err != nil {
		return TOTPFailureWindow{}, fmt.Errorf("state: read TOTP failure window: %w", err)
	}
	return TOTPFailureWindow{Failures: int(row.Failures), Oldest: row.Oldest.Time, CheckedAt: row.CheckedAt.Time}, nil
}

func (s *PgStore) RecordTOTPFailure(ctx context.Context, accountID string, window time.Duration) error {
	id, err := totpAccountUUID(accountID)
	if err != nil {
		return err
	}
	secs, err := totpWindowSeconds(window)
	if err != nil {
		return err
	}
	if err := sqlc.New().RecordTOTPFailure(ctx, s.pool, sqlc.RecordTOTPFailureParams{AccountID: id, WindowSeconds: secs}); err != nil {
		return fmt.Errorf("state: record TOTP failure: %w", err)
	}
	return nil
}

func (s *PgStore) ResetTOTPFailures(ctx context.Context, accountID string) error {
	id, err := totpAccountUUID(accountID)
	if err != nil {
		return err
	}
	if err := sqlc.New().ResetTOTPFailures(ctx, s.pool, id); err != nil {
		return fmt.Errorf("state: reset TOTP failures: %w", err)
	}
	return nil
}

// MemStore mirrors the SQL with time.Now as the database clock.

func (m *MemStore) TOTPFailures(_ context.Context, accountID string, window time.Duration) (TOTPFailureWindow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := TOTPFailureWindow{Oldest: now, CheckedAt: now}
	for _, at := range m.totpFailures[accountID] {
		if now.Sub(at) < window {
			if out.Failures == 0 || at.Before(out.Oldest) {
				out.Oldest = at
			}
			out.Failures++
		}
	}
	return out, nil
}

func (m *MemStore) RecordTOTPFailure(_ context.Context, accountID string, window time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.totpFailures == nil {
		m.totpFailures = map[string][]time.Time{}
	}
	now := time.Now()
	kept := m.totpFailures[accountID][:0:0]
	for _, at := range m.totpFailures[accountID] {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	m.totpFailures[accountID] = append(kept, now)
	return nil
}

func (m *MemStore) ResetTOTPFailures(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.totpFailures, accountID)
	return nil
}

var _ TOTPFailureStore = (*PgStore)(nil)
var _ TOTPFailureStore = (*MemStore)(nil)
