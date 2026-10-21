package mailoutbox

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

var ErrPasswordResetUnknown = errors.New("password-reset credential not found")
var ErrPasswordResetInvalid = errors.New("password-reset credential unavailable")

// ResetCredentials enforces already-issued credentials even when issuance and
// delivery flags are rolled off. Constructing it reads no database or keys.
type ResetCredentials struct{ pool *pgxpool.Pool }

func NewResetCredentials(pool *pgxpool.Pool) *ResetCredentials {
	if pool == nil {
		return nil
	}
	return &ResetCredentials{pool: pool}
}

// Redeem changes the password and consumes exactly one reset token in a single
// owner transaction. New reset hashes are never inserted into login_tokens.
// Account-first locking also makes two sibling links produce at most one winner.
func (c *ResetCredentials) Redeem(ctx context.Context, hash []byte, phc string) (string, error) {
	return c.redeem(ctx, hash, phc, nil)
}

func (c *ResetCredentials) redeem(ctx context.Context, hash []byte, phc string, alert *Service) (string, error) {
	if c == nil || len(hash) != api.PasswordResetTokenBytes || phc == "" {
		return "", internalmail.ErrInvalid
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer rollback(ctx, tx)
	queries := q.New()
	if alert != nil {
		if !alert.passwordChangedAlertEnabled() {
			return "", internalmail.ErrDisabled
		}
		if err := queries.LockAdmission(ctx, tx, alert.namespace); err != nil {
			return "", err
		}
	}
	account, err := queries.LockPasswordResetCredentialAccount(ctx, tx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPasswordResetUnknown
	}
	if err != nil {
		return "", err
	}
	token, err := queries.LockPasswordResetToken(ctx, tx, q.LockPasswordResetTokenParams{TokenHash: hash, AccountID: account.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPasswordResetInvalid
	}
	if err != nil {
		return "", err
	}
	if alert != nil && token.Namespace != alert.namespace {
		return "", ErrPasswordResetInvalid
	}
	if err := queries.ShareRecovery(ctx, tx, token.Namespace); err != nil {
		return "", err
	}
	recovery, err := queries.Recovery(ctx, tx, token.Namespace)
	if err != nil {
		return "", err
	}
	if recovery.Held || recovery.RecoveryID != token.RecoveryID {
		return "", ErrPasswordResetInvalid
	}
	now, err := queries.VerificationClock(ctx, tx)
	if err != nil {
		return "", err
	}
	if account.Status != "active" || !account.EmailVerifiedAt.Valid || !strings.EqualFold(account.Email, token.EmailAddress) || account.PasswordResetRevision != token.PasswordRevision || token.ConsumedAt.Valid || !token.ExpiresAt.Time.After(now.Time) {
		return "", ErrPasswordResetInvalid
	}
	n, err := queries.ReplaceResetPassword(ctx, tx, q.ReplaceResetPasswordParams{ID: account.ID, PasswordResetRevision: token.PasswordRevision, Hash: phc})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrPasswordResetInvalid
	}
	if alert != nil {
		now, err := queries.VerificationClock(ctx, tx)
		if err != nil {
			return "", err
		}
		if err := alert.savePasswordChangedAlert(ctx, tx, account.ID, account.Email, recovery.RecoveryID, now.Time, "password_reset"); err != nil {
			return "", err
		}
	}
	// Recheck the absolute database deadline after password-row and alert waits. A
	// late expiry rolls back both the credential update and token consumption.
	n, err = queries.ConsumePasswordResetToken(ctx, tx, q.ConsumePasswordResetTokenParams{TokenHash: hash, AccountID: account.ID})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrPasswordResetInvalid
	}
	if alert != nil && !alert.passwordChangedAlertEnabled() {
		return "", internalmail.ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return uuidString(account.ID.Bytes), nil
}
