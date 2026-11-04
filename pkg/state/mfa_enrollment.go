package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// MFAEnrollmentStore binds setup and confirmation to the current pending
// factor. Confirmation spends the TOTP step in the same atomic write.
type MFAEnrollmentStore interface {
	SavePendingMFASecret(context.Context, string, []byte, [][]byte) (bool, error)
	ConfirmPendingMFA(context.Context, string, []byte, int64) (bool, error)
}

var _ MFAEnrollmentStore = (*MemStore)(nil)
var _ MFAEnrollmentStore = (*PgStore)(nil)

func (s *PgStore) SavePendingMFASecret(ctx context.Context, id string, encrypted []byte, hashes [][]byte) (bool, error) {
	account, err := parsePgUUID(id)
	if err != nil {
		return false, err
	}
	_, err = sqlc.New().SavePendingMFASecret(ctx, s.pool, sqlc.SavePendingMFASecretParams{
		ID: account, MfaSecretEncrypted: encrypted, MfaRecoveryCodesHash: hashes,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: save pending MFA: %w", err)
	}
	return true, nil
}

func (s *PgStore) ConfirmPendingMFA(ctx context.Context, id string, encrypted []byte, step int64) (bool, error) {
	account, err := parsePgUUID(id)
	if err != nil {
		return false, err
	}
	_, err = sqlc.New().ConfirmPendingMFA(ctx, s.pool, sqlc.ConfirmPendingMFAParams{
		ID: account, MfaSecretEncrypted: encrypted, Step: step,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: confirm pending MFA: %w", err)
	}
	return true, nil
}

func (m *MemStore) SavePendingMFASecret(_ context.Context, id string, encrypted []byte, hashes [][]byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok || a.MFAEnrolled() {
		return false, nil
	}
	a.MFASecretEncrypted = slices.Clone(encrypted)
	a.MFARecoveryCodesHash = make([][]byte, len(hashes))
	for i, hash := range hashes {
		a.MFARecoveryCodesHash[i] = slices.Clone(hash)
	}
	delete(m.totpLastSteps, id)
	m.storeRouteCheckAccountLocked(id, a)
	m.consumeMFADisableRequestsLocked(id)
	return true, nil
}

func (m *MemStore) ConfirmPendingMFA(_ context.Context, id string, encrypted []byte, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok || a.MFAEnrolled() || len(encrypted) == 0 || !bytes.Equal(a.MFASecretEncrypted, encrypted) {
		return false, nil
	}
	if last, seen := m.totpLastSteps[id]; seen && last >= step {
		return false, nil
	}
	if m.totpLastSteps == nil {
		m.totpLastSteps = make(map[string]int64)
	}
	m.totpLastSteps[id] = step
	now := time.Now().UTC()
	a.MFAEnrolledAt, a.MFARequired = &now, false
	m.storeRouteCheckAccountLocked(id, a)
	m.consumeMFADisableRequestsLocked(id)
	return true, nil
}
