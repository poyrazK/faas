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

var ErrMagicLinkUnknown = errors.New("magic-link credential not found")
var ErrMagicLinkInvalid = errors.New("magic-link credential unavailable")

// MagicLinkCredentials enforces saved sign-in credentials after flag rollback.
// Construction reads no database, environment, encryption key or socket.
type MagicLinkCredentials struct{ pool *pgxpool.Pool }

type MagicLinkRedemption struct {
	AccountID                       string
	RevokedSessions, RevokedAPIKeys int64
}

func NewMagicLinkCredentials(pool *pgxpool.Pool) *MagicLinkCredentials {
	if pool == nil {
		return nil
	}
	return &MagicLinkCredentials{pool: pool}
}

// Match the existing sign-in account policy, including billing grace periods.
func magicLinkAccountAvailable(status string, abuseHold pgtype.Timestamptz) bool {
	return (status == "active" || status == "past_due") && !abuseHold.Valid
}

func magicLinkAvailable(email, status string, verified, abuseHold pgtype.Timestamptz, revision int64, token q.MagicLinkToken, now time.Time) bool {
	return magicLinkAccountAvailable(status, abuseHold) && strings.EqualFold(email, token.EmailAddress) && revision == token.PasswordRevision &&
		verified.Valid == token.EmailVerifiedAt.Valid && (!verified.Valid || verified.Time.Equal(token.EmailVerifiedAt.Time)) &&
		!token.ConsumedAt.Valid && token.ExpiresAt.Time.After(now)
}

// Redeem locks account before token, checks the saved recovery generation, and
// consumes only a sign-in credential. For a new address, ownership proof also
// removes pre-verification passwords, sessions and keys in the same transaction.
// MFA policy and dashboard session creation remain with the existing caller.
func (c *MagicLinkCredentials) Redeem(ctx context.Context, hash []byte) (MagicLinkRedemption, error) {
	var result MagicLinkRedemption
	if c == nil || len(hash) != api.MagicLinkTokenBytes {
		return result, internalmail.ErrInvalid
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer rollback(ctx, tx)
	queries := q.New()
	account, err := queries.LockMagicLinkCredentialAccount(ctx, tx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrMagicLinkUnknown
	}
	if err != nil {
		return result, err
	}
	token, err := queries.LockMagicLinkToken(ctx, tx, q.LockMagicLinkTokenParams{TokenHash: hash, AccountID: account.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrMagicLinkInvalid
	}
	if err != nil {
		return result, err
	}
	if err := queries.ShareRecovery(ctx, tx, token.Namespace); err != nil {
		return result, err
	}
	recovery, err := queries.Recovery(ctx, tx, token.Namespace)
	if err != nil {
		return result, err
	}
	if recovery.Held || recovery.RecoveryID != token.RecoveryID {
		return result, ErrMagicLinkInvalid
	}
	now, err := queries.VerificationClock(ctx, tx)
	if err != nil {
		return result, err
	}
	if !magicLinkAvailable(account.Email, account.Status, account.EmailVerifiedAt, account.AbuseHoldAt, account.PasswordResetRevision, token, now.Time) {
		return result, ErrMagicLinkInvalid
	}
	if !account.EmailVerifiedAt.Valid {
		result, err = claimMagicLinkOwnership(ctx, queries, tx, account.ID)
		if err != nil {
			return MagicLinkRedemption{}, err
		}
	}
	// Deadline is checked after any cleanup-row waits. Failure rolls back every
	// cleanup write and leaves the token unused; a retry never extends expiry.
	n, err := queries.ConsumeMagicLinkToken(ctx, tx, q.ConsumeMagicLinkTokenParams{TokenHash: hash, AccountID: account.ID})
	if err != nil {
		return MagicLinkRedemption{}, err
	}
	if n != 1 {
		return MagicLinkRedemption{}, ErrMagicLinkInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return MagicLinkRedemption{}, err
	}
	result.AccountID = uuidString(account.ID.Bytes)
	return result, nil
}

func claimMagicLinkOwnership(ctx context.Context, queries *q.Queries, tx pgx.Tx, account pgtype.UUID) (MagicLinkRedemption, error) {
	var result MagicLinkRedemption
	if err := queries.DeleteMagicLinkPassword(ctx, tx, account); err != nil {
		return result, err
	}
	var err error
	result.RevokedSessions, err = queries.RevokeMagicLinkSessions(ctx, tx, account)
	if err != nil {
		return result, err
	}
	result.RevokedAPIKeys, err = queries.DeleteMagicLinkAPIKeys(ctx, tx, account)
	if err != nil {
		return result, err
	}
	if err := queries.ClaimMagicLinkOwnership(ctx, tx, account); err != nil {
		return result, err
	}
	return result, nil
}
