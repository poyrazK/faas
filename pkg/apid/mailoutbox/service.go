package mailoutbox

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

var ErrAccountUnavailable = errors.New("notification account cannot enter deletion")

func pgID(value string) pgtype.UUID { var id pgtype.UUID; _ = id.Scan(value); return id }

func rollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.InternalMailOperatorRollbackTimeout)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

func (s *Service) recovery(ctx context.Context, tx pgx.Tx) (q.RecoveryRow, error) {
	if err := s.queries.ShareRecovery(ctx, tx, s.namespace); err != nil {
		return q.RecoveryRow{}, err
	}
	return s.queries.Recovery(ctx, tx, s.namespace)
}

// ScheduleDeletion makes the business transition and notification intent one
// commit. The admission fence bounds pending work across all apid instances.
// Repeating a previously saved transition leaves its original intent intact.
func (s *Service) ScheduleDeletion(ctx context.Context, id string, opts ...state.AccountDeletionOption) error {
	if !s.Enabled() {
		return internalmail.ErrDisabled
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountUnavailable
	}
	if err != nil {
		return err
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	if account.Status != "active" && account.Status != "deleted_pending" {
		return ErrAccountUnavailable
	}
	if account.Status == "deleted_pending" && account.DeletionRequestedAt.Valid {
		_, err := s.queries.ExistingIntent(ctx, tx, q.ExistingIntentParams{AccountID: account.ID, DeletionRequestedAt: account.DeletionRequestedAt})
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Never backfill an unobserved old deletion after enabling the flag.
		return ErrAccountUnavailable
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return internalmail.ErrQuota
	}
	recipient, err := internalmail.NormalizeSuppressionAddress(account.Email)
	if err != nil {
		return err
	}
	scheduled, err := s.queries.ScheduleDeletion(ctx, tx, q.ScheduleDeletionParams{ID: account.ID, IncludeManagedPostgres: state.DeletionIncludesManagedPostgres(opts...)})
	if err != nil {
		return err
	}
	if err := s.queries.CreateIntent(ctx, tx, q.CreateIntentParams{ID: pgID(uuid.NewString()), AccountID: account.ID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: recipient,
		DeletionRequestedAt: scheduled.DeletionRequestedAt, GraceSeconds: int32(api.AccountDeletionGraceDays * 24 * time.Hour / time.Second), TtlSeconds: int32(api.InternalMailProducerOutboxTTL / time.Second), RecoveryID: recovery.RecoveryID}); err != nil {
		return err
	}
	if !s.Enabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
