package mailoutbox

import (
	"context"
	"errors"
	"strings"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

var (
	ErrMFAEnrollmentUnavailable       = errors.New("MFA enrollment unavailable")
	ErrMFAEnrollmentAlreadyEnabled    = errors.New("MFA enrollment already confirmed")
	ErrMFAEnrollmentInvalidCode       = errors.New("MFA enrollment code unavailable")
	ErrMFAEnrollmentSecretUnavailable = errors.New("MFA enrollment secret unavailable")
)

// SavePendingMFAEnrollment saves setup material without emitting an alert. Its
// conditional write refuses stale setup requests that would replace a confirmed
// authenticator. It never clears enrollment or returns existing recovery codes.
func (s *Service) SavePendingMFAEnrollment(ctx context.Context, id, email string, sealed []byte, hashes [][]byte) error {
	if !s.mfaEnabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	if len(sealed) == 0 || len(hashes) != authcode.RecoveryCodeCount {
		return internalmail.ErrInvalid
	}
	for _, hash := range hashes {
		if len(hash) != authcode.HMACSize {
			return internalmail.ErrInvalid
		}
	}
	tx, account, err := s.beginMFAEnabled(ctx, id, email)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	defer clear(account.MfaSecretEncrypted)
	if account.MfaEnrolledAt.Valid {
		return ErrMFAEnrollmentAlreadyEnabled
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	n, err := s.queries.SavePendingMFAEnrollment(ctx, tx, q.SavePendingMFAEnrollmentParams{ID: account.ID, MfaSecretEncrypted: sealed, MfaRecoveryCodesHash: hashes})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrMFAEnrollmentAlreadyEnabled
	}
	if !s.mfaEnabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

// ConfirmMFAEnrollmentWithAlert verifies the locked factor, then commits its
// enrollment timestamp/policy/revision and alert together. TOTP is checked after
// lock waits and again after saving intent, so a blocked save cannot extend proof.
// The host readers are for authentication only; no host secret enters the alert.
func (s *Service) ConfirmMFAEnrollmentWithAlert(ctx context.Context, id, email, code string, identities []*age.X25519Identity) error {
	if !s.mfaEnabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	tx, account, err := s.beginMFAEnabled(ctx, id, email)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	defer clear(account.MfaSecretEncrypted)
	if len(account.MfaSecretEncrypted) == 0 {
		return ErrMFAEnrollmentUnavailable
	}
	envelope, err := secretbox.OpenMulti(identities, account.MfaSecretEncrypted)
	if err != nil || envelope["MFA_SECRET"] == "" {
		return ErrMFAEnrollmentSecretUnavailable
	}
	defer clear(envelope)
	secret := envelope["MFA_SECRET"]
	if !auth.VerifyCode(secret, code) {
		return ErrMFAEnrollmentInvalidCode
	}
	if account.MfaEnrolledAt.Valid {
		// Already-confirmed accounts still prove possession before session clearance.
		// Do not backfill an old enrollment or rewrite its timestamp on repeat calls.
		if !s.mfaEnabledAlertEnabled() {
			return internalmail.ErrDisabled
		}
		return nil
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	at, err := s.queries.ConfirmMFAEnabledEnrollment(ctx, tx, account.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMFAEnrollmentUnavailable
	}
	if err != nil {
		return err
	}
	if err := s.saveMFAEnabledAlert(ctx, tx, account.ID, account.Email, recovery.RecoveryID, at.Time); err != nil {
		return err
	}
	if !s.mfaEnabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	if !auth.VerifyCode(secret, code) {
		return ErrMFAEnrollmentInvalidCode
	}
	return tx.Commit(ctx)
}

func (s *Service) beginMFAEnabled(ctx context.Context, id, email string) (pgx.Tx, q.LockMFAEnabledEnrollmentAccountRow, error) {
	var account q.LockMFAEnabledEnrollmentAccountRow
	if !s.mfaEnabledAlertEnabled() {
		return nil, account, internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil || !validID(id) {
		return nil, account, ErrMFAEnrollmentUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, account, err
	}
	if err = s.queries.LockAdmission(ctx, tx, s.namespace); err == nil {
		account, err = s.queries.LockMFAEnabledEnrollmentAccount(ctx, tx, pgID(id))
	}
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !strings.EqualFold(account.Email, address)) {
		err = ErrMFAEnrollmentUnavailable
	}
	if err != nil {
		rollback(ctx, tx)
		clear(account.MfaSecretEncrypted)
		return nil, account, err
	}
	return tx, account, nil
}
