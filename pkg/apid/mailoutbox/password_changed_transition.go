package mailoutbox

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

var (
	ErrPasswordChangeInvalid     = errors.New("password change proof unavailable")
	ErrPasswordChangeMFARequired = errors.New("password change requires MFA enrollment")
	ErrPasswordChangeStepUp      = errors.New("password change requires MFA step-up")
)

// PasswordChange is an opaque, revision-bound authorization. It contains no
// password or hash and cannot be constructed by callers to bypass the proof.
type PasswordChange struct {
	owner                         *Service
	id, email                     string
	passwordRevision, mfaRevision int64
	proof                         string
	replacing                     bool
}

func (c *PasswordChange) Proof() string   { return c.proof }
func (c *PasswordChange) Replacing() bool { return c.replacing }

// PreparePasswordChange preserves the dashboard's proof policy. The caller
// authenticates the session and CSRF, and checks the existing step-up window.
// Argon verification happens before any owner locks, then revisions are rechecked.
func (s *Service) PreparePasswordChange(ctx context.Context, id, email, currentPassword string, freshStepUp bool) (*PasswordChange, error) {
	if !s.passwordChangedAlertEnabled() {
		return nil, internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil || !validID(id) {
		return nil, ErrPasswordChangeInvalid
	}
	account, err := s.queries.PasswordChangeProof(ctx, s.pool, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPasswordChangeInvalid
	}
	if err != nil {
		return nil, err
	}
	if !magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !strings.EqualFold(account.Email, address) {
		return nil, ErrPasswordChangeInvalid
	}
	proof := "session"
	switch {
	case freshStepUp:
		proof = "step_up"
	case account.MfaRequired && !account.MfaEnrolledAt.Valid:
		return nil, ErrPasswordChangeMFARequired
	case account.MfaEnrolledAt.Valid:
		return nil, ErrPasswordChangeStepUp
	case account.HasPassword:
		matched, err := auth.Verify(account.Hash, currentPassword)
		if err != nil || !matched {
			return nil, ErrPasswordChangeInvalid
		}
		proof = "current_password"
	}
	return &PasswordChange{owner: s, id: id, email: address, passwordRevision: account.PasswordResetRevision, mfaRevision: account.MfaRevision, proof: proof, replacing: account.HasPassword}, nil
}

// CommitPasswordChange atomically updates the password and saves the alert.
// It refuses stale password/MFA proofs and late gate withdrawals without writes.
func (s *Service) CommitPasswordChange(ctx context.Context, change *PasswordChange, phc string) error {
	if !s.passwordChangedAlertEnabled() {
		return internalmail.ErrDisabled
	}
	if change == nil || change.owner != s || phc == "" {
		return ErrPasswordChangeInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockPasswordChangeAccount(ctx, tx, pgID(change.id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPasswordChangeInvalid
	}
	if err != nil {
		return err
	}
	if !magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !strings.EqualFold(account.Email, change.email) || account.PasswordResetRevision != change.passwordRevision || account.MfaRevision != change.mfaRevision {
		return ErrPasswordChangeInvalid
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	n, err := s.queries.ReplaceResetPassword(ctx, tx, q.ReplaceResetPasswordParams{ID: account.ID, PasswordResetRevision: account.PasswordResetRevision, Hash: phc})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPasswordChangeInvalid
	}
	now, err := s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return err
	}
	if err := s.savePasswordChangedAlert(ctx, tx, account.ID, account.Email, recovery.RecoveryID, now.Time, "dashboard"); err != nil {
		return err
	}
	if !s.passwordChangedAlertEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPasswordWithAlert(ctx context.Context, hash []byte, phc string) (string, error) {
	if !s.passwordChangedAlertEnabled() {
		return "", internalmail.ErrDisabled
	}
	return NewResetCredentials(s.pool).redeem(ctx, hash, phc, s)
}

func (s *Service) ResetLegacyPasswordWithAlert(ctx context.Context, hash []byte, phc string) (string, error) {
	if !s.passwordChangedAlertEnabled() {
		return "", internalmail.ErrDisabled
	}
	if len(hash) != 32 || phc == "" {
		return "", ErrPasswordResetInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return "", err
	}
	account, err := s.queries.LockLegacyPasswordResetAccount(ctx, tx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPasswordResetInvalid
	}
	if err != nil {
		return "", err
	}
	token, err := s.queries.LockLegacyPasswordResetToken(ctx, tx, q.LockLegacyPasswordResetTokenParams{TokenHash: hash, AccountID: account.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPasswordResetInvalid
	}
	if err != nil {
		return "", err
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return "", err
	}
	if recovery.Held {
		return "", internalmail.ErrRecoveryHeld
	}
	now, err := s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return "", err
	}
	if account.Status != "active" || !account.EmailVerifiedAt.Valid || token.ConsumedAt.Valid || !token.ExpiresAt.Time.After(now.Time) {
		return "", ErrPasswordResetInvalid
	}
	n, err := s.queries.ReplaceResetPassword(ctx, tx, q.ReplaceResetPasswordParams{ID: account.ID, PasswordResetRevision: account.PasswordResetRevision, Hash: phc})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrPasswordResetInvalid
	}
	now, err = s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return "", err
	}
	if err := s.savePasswordChangedAlert(ctx, tx, account.ID, account.Email, recovery.RecoveryID, now.Time, "password_reset"); err != nil {
		return "", err
	}
	// Run the absolute expiry CAS after both the password row and alert insert
	// have finished waiting. Expiry rolls back all three effects.
	n, err = s.queries.ConsumeLegacyPasswordResetToken(ctx, tx, q.ConsumeLegacyPasswordResetTokenParams{TokenHash: hash, AccountID: account.ID})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrPasswordResetInvalid
	}
	if !s.passwordChangedAlertEnabled() {
		return "", internalmail.ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return uuidString(account.ID.Bytes), nil
}
