package mailoutbox

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// Password verification happens before owner locks. The locked account revisions
// must still match that proof; changed passwords or factors cannot reuse it.
func (s *Service) DisableMFAPasswordWithAlert(ctx context.Context, id, email, password string) error {
	if !s.mfaDisabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(id) || password == "" {
		return ErrMFADisableInvalid
	}
	proof, err := s.queries.MFADisabledPasswordProof(ctx, s.pool, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMFADisableInvalid
	}
	if err != nil {
		return err
	}
	matched, err := auth.Verify(proof.Hash, password)
	if err != nil || !matched {
		return ErrMFADisableInvalid
	}
	tx, account, err := s.beginMFADisabled(ctx, id, email)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	defer clearDisabledRecoveryHashes(account.MfaRecoveryCodesHash)
	if account.PasswordResetRevision != proof.PasswordResetRevision || account.MfaRevision != proof.MfaRevision {
		return ErrMFADisableInvalid
	}
	return s.completeMFADisabled(ctx, tx, account, "password", nil)
}

// The final recovery code is allowed: clearing the factor consumes all its
// codes in the same transaction as the alert. Failure leaves every code intact.
func (s *Service) DisableMFARecoveryWithAlert(ctx context.Context, id, email string, presented []byte) error {
	if !s.mfaDisabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	if len(presented) != authcode.HMACSize {
		return ErrMFADisableInvalid
	}
	tx, account, err := s.beginMFADisabled(ctx, id, email)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	defer clearDisabledRecoveryHashes(account.MfaRecoveryCodesHash)
	matches := 0
	for _, hash := range account.MfaRecoveryCodesHash {
		if len(hash) != authcode.HMACSize {
			return ErrMFADisableInvalid
		}
		matches += subtle.ConstantTimeCompare(hash, presented)
	}
	if matches != 1 || len(account.MfaRecoveryCodesHash) > authcode.RecoveryCodeCount {
		return ErrMFADisableInvalid
	}
	return s.completeMFADisabled(ctx, tx, account, "recovery_code", nil)
}

// The HTTP boundary authenticates the legacy age-sealed envelope before calling
// this method. Its account binding and cooldown are rechecked under owner locks.
func (s *Service) ConfirmLegacyMFADisableWithAlert(ctx context.Context, id, email string, hash []byte) (time.Time, error) {
	if !s.mfaDisabledAlertEnabled() {
		return time.Time{}, internalmail.ErrDisabled
	}
	if len(hash) != api.MFADisableTokenBytes {
		return time.Time{}, ErrMFADisableInvalid
	}
	tx, account, err := s.beginMFADisabled(ctx, id, email)
	if err != nil {
		return time.Time{}, err
	}
	defer rollback(ctx, tx)
	defer clearDisabledRecoveryHashes(account.MfaRecoveryCodesHash)
	token, err := s.queries.LockLegacyMFADisableRequest(ctx, tx, q.LockLegacyMFADisableRequestParams{TokenHash: hash, AccountID: account.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrMFADisableInvalid
	}
	if err != nil {
		return time.Time{}, err
	}
	if token.ConsumedAt.Valid || account.MfaEnrolledAt.Time.After(token.RequestedAt.Time) {
		return time.Time{}, ErrMFADisableInvalid
	}
	ready := token.RequestedAt.Time.Add(api.MFADisableEmailCooldown)
	err = s.completeMFADisabled(ctx, tx, account, "email", func(now time.Time) error {
		if now.Before(ready) {
			return ErrMFADisableCooldown
		}
		n, err := s.queries.ConsumeLegacyMFADisableRequest(ctx, tx, q.ConsumeLegacyMFADisableRequestParams{TokenHash: hash, AccountID: account.ID})
		if err == nil && n != 1 {
			return ErrMFADisableInvalid
		}
		return err
	})
	return ready, err
}

func (s *Service) ConfirmMFADisableWithAlert(ctx context.Context, id string, hash []byte) (time.Time, error) {
	if !s.mfaDisabledAlertEnabled() {
		return time.Time{}, internalmail.ErrDisabled
	}
	return NewMFADisableCredentials(s.pool).redeem(ctx, id, hash, s)
}

func (s *Service) beginMFADisabled(ctx context.Context, id, email string) (pgx.Tx, q.LockMFADisabledAccountRow, error) {
	var account q.LockMFADisabledAccountRow
	if !s.mfaDisabledAlertEnabled() {
		return nil, account, internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil || !validID(id) {
		return nil, account, ErrMFADisableInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, account, err
	}
	if err = s.queries.LockAdmission(ctx, tx, s.namespace); err == nil {
		account, err = s.queries.LockMFADisabledAccount(ctx, tx, pgID(id))
	}
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !account.MfaEnrolledAt.Valid || !strings.EqualFold(account.Email, address)) {
		err = ErrMFADisableInvalid
	}
	if err != nil {
		rollback(ctx, tx)
		clearDisabledRecoveryHashes(account.MfaRecoveryCodesHash)
		return nil, account, err
	}
	return tx, account, nil
}

func (s *Service) completeMFADisabled(ctx context.Context, tx pgx.Tx, account q.LockMFADisabledAccountRow, method string, consume func(time.Time) error) error {
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	now, err := s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return err
	}
	if consume != nil {
		if err := consume(now.Time); err != nil {
			return err
		}
	}
	if err := s.queries.ClearMFADisableFactor(ctx, tx, account.ID); err != nil {
		return err
	}
	if err := s.saveMFADisabledAlert(ctx, tx, account.ID, account.Email, recovery.RecoveryID, now.Time, method); err != nil {
		return err
	}
	if !s.mfaDisabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func clearDisabledRecoveryHashes(hashes [][]byte) {
	for _, hash := range hashes {
		clear(hash)
	}
}
