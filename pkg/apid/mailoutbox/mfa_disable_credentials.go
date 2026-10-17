package mailoutbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

var ErrMFADisableUnknown = errors.New("MFA disable credential not found")
var ErrMFADisableInvalid = errors.New("MFA disable credential unavailable")
var ErrMFADisableCooldown = errors.New("MFA disable cooldown active")

// MFADisableCredentials enforces saved links after issuance/delivery rollback.
// Construction reads no database, environment, key or socket.
type MFADisableCredentials struct{ pool *pgxpool.Pool }

func NewMFADisableCredentials(pool *pgxpool.Pool) *MFADisableCredentials {
	if pool == nil {
		return nil
	}
	return &MFADisableCredentials{pool: pool}
}

func mfaDisableAvailable(email, status string, abuse pgtype.Timestamptz, passwordRevision, mfaRevision int64, enrolled pgtype.Timestamptz, token q.MfaDisableToken, now time.Time) bool {
	return magicLinkAccountAvailable(status, abuse) && strings.EqualFold(email, token.EmailAddress) &&
		passwordRevision == token.PasswordRevision && mfaRevision == token.MfaRevision && enrolled.Valid && enrolled.Time.Equal(token.MfaEnrolledAt.Time) &&
		!token.ConsumedAt.Valid && token.ExpiresAt.Time.After(now)
}

// Redeem locks account before token and recovery, then clears only the bound
// enrolled factor and consumes the token in one commit. It neither authenticates
// a session nor clears the account's explicit MFA policy.
func (c *MFADisableCredentials) Redeem(ctx context.Context, accountID string, hash []byte) (time.Time, error) {
	return c.redeem(ctx, accountID, hash, nil)
}

func (c *MFADisableCredentials) redeem(ctx context.Context, accountID string, hash []byte, alert *Service) (time.Time, error) {
	if alert != nil && !alert.mfaDisabledAlertEnabled() {
		return time.Time{}, internalmail.ErrDisabled
	}
	if c == nil || !validID(accountID) || len(hash) != api.MFADisableTokenBytes {
		return time.Time{}, internalmail.ErrInvalid
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer rollback(ctx, tx)
	queries := q.New()
	if alert != nil {
		if err := queries.LockAdmission(ctx, tx, alert.namespace); err != nil {
			return time.Time{}, err
		}
	}
	account, err := queries.LockMFADisableCredentialAccount(ctx, tx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrMFADisableUnknown
	}
	if err != nil {
		return time.Time{}, err
	}
	if account.ID != pgID(accountID) {
		return time.Time{}, ErrMFADisableInvalid
	}
	token, err := queries.LockMFADisableToken(ctx, tx, q.LockMFADisableTokenParams{TokenHash: hash, AccountID: account.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrMFADisableInvalid
	}
	if err != nil {
		return time.Time{}, err
	}
	if alert != nil && token.Namespace != alert.namespace {
		return time.Time{}, ErrMFADisableInvalid
	}
	if err := queries.ShareRecovery(ctx, tx, token.Namespace); err != nil {
		return time.Time{}, err
	}
	recovery, err := queries.Recovery(ctx, tx, token.Namespace)
	if err != nil {
		return time.Time{}, err
	}
	if recovery.Held || recovery.RecoveryID != token.RecoveryID {
		return time.Time{}, ErrMFADisableInvalid
	}
	now, err := queries.VerificationClock(ctx, tx)
	if err != nil {
		return time.Time{}, err
	}
	if !mfaDisableAvailable(account.Email, account.Status, account.AbuseHoldAt, account.PasswordResetRevision, account.MfaRevision, account.MfaEnrolledAt, token, now.Time) {
		return time.Time{}, ErrMFADisableInvalid
	}
	if now.Time.Before(token.ReadyAt.Time) {
		return token.ReadyAt.Time, ErrMFADisableCooldown
	}
	if err := queries.ClearMFADisableFactor(ctx, tx, account.ID); err != nil {
		return time.Time{}, err
	}
	if alert != nil {
		if err := alert.saveMFADisabledAlert(ctx, tx, account.ID, account.Email, recovery.RecoveryID, now.Time, "email"); err != nil {
			return time.Time{}, err
		}
	}
	n, err := queries.ConsumeMFADisableToken(ctx, tx, q.ConsumeMFADisableTokenParams{TokenHash: hash, AccountID: account.ID})
	if err != nil {
		return time.Time{}, err
	}
	if n != 1 {
		return time.Time{}, ErrMFADisableInvalid
	}
	if alert != nil && !alert.mfaDisabledAlertEnabled() {
		return time.Time{}, internalmail.ErrDisabled
	}
	return time.Time{}, tx.Commit(ctx)
}
