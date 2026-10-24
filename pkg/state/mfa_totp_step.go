package state

import (
	"context"
	"fmt"
)

// TOTPStepRecorder records the time step of the last TOTP code an account
// used to gain a session, so the same code cannot be replayed within its
// validity window (RFC 6238 §5.2).
type TOTPStepRecorder interface {
	// AcceptTOTPStep stores step as the account's last accepted step and
	// reports true, or reports false without writing when a code at or
	// after step was already accepted. The compare and the write are one
	// atomic statement, so two concurrent requests with one code cannot
	// both be accepted.
	AcceptTOTPStep(ctx context.Context, accountID string, step int64) (bool, error)
}

var _ TOTPStepRecorder = (*PgStore)(nil)
var _ TOTPStepRecorder = (*MemStore)(nil)

func (s *PgStore) AcceptTOTPStep(ctx context.Context, accountID string, step int64) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		update accounts
		   set mfa_totp_last_step = $2
		 where id = $1
		   and (mfa_totp_last_step is null or mfa_totp_last_step < $2)`, account, step)
	if err != nil {
		return false, fmt.Errorf("state: accept totp step: %w", mapErr(err))
	}
	return tag.RowsAffected() == 1, nil
}

func (m *MemStore) AcceptTOTPStep(_ context.Context, accountID string, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return false, nil
	}
	if last, seen := m.totpLastSteps[accountID]; seen && last >= step {
		return false, nil
	}
	if m.totpLastSteps == nil {
		m.totpLastSteps = make(map[string]int64)
	}
	m.totpLastSteps[accountID] = step
	return true, nil
}
