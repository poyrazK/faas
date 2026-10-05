// adr:438
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func lockAppSecretRuntimeProcess(ctx context.Context, tx pgx.Tx, p AppSecretRuntimeProcess) (sqlc.LockAppSecretRuntimeProcessRow, error) {
	q := sqlc.New()
	if err := lockSecretRuntimeApp(ctx, tx, p.AccountID, p.AppID); err != nil {
		return sqlc.LockAppSecretRuntimeProcessRow{}, err
	}

	_, err := q.EnsureAppSecretRuntimeProcess(ctx, tx, sqlc.EnsureAppSecretRuntimeProcessParams{
		AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(p.AppID), InstanceID: mustPgUUID(p.InstanceID), WorkloadName: p.WorkloadName,
	})
	if err != nil {
		return sqlc.LockAppSecretRuntimeProcessRow{}, mapErr(err)
	}
	row, err := q.LockAppSecretRuntimeProcess(ctx, tx, sqlc.LockAppSecretRuntimeProcessParams{
		AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(p.AppID), InstanceID: mustPgUUID(p.InstanceID), WorkloadName: p.WorkloadName,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrConflict
	}
	return row, mapErr(err)
}

func (s *PgStore) BeginAppSecretRuntimeProcess(ctx context.Context, p AppSecretRuntimeProcess) error {
	return s.transitionAppSecretRuntimeProcess(ctx, p, true)
}

func (s *PgStore) RetireAppSecretRuntimeProcess(ctx context.Context, p AppSecretRuntimeProcess) error {
	if p.PreviousGeneration != "" {
		return ErrInvalidArgument
	}
	return s.transitionAppSecretRuntimeProcess(ctx, p, false)
}

func (s *PgStore) transitionAppSecretRuntimeProcess(ctx context.Context, p AppSecretRuntimeProcess, active bool) error {
	if !validAppSecretRuntimeProcess(p) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, err := lockAppSecretRuntimeProcess(ctx, tx, p)
	if err != nil {
		return err
	}
	if current.Generation == p.Generation && current.Active == active {
		return tx.Commit(ctx)
	}
	if !active && current.Generation != p.Generation || active && (current.Generation == p.Generation || current.Generation != "" && current.Generation != p.PreviousGeneration) {
		return ErrConflict
	}
	started := current.StartedAt
	if active {
		at := p.AttemptedAt.UTC()
		if at.IsZero() {
			at = time.Now().UTC()
		}
		started = pgtype.Timestamptz{Time: at, Valid: true}
	}
	q := sqlc.New()
	_, err = q.SetAppSecretRuntimeProcess(ctx, tx, sqlc.SetAppSecretRuntimeProcessParams{AppID: mustPgUUID(p.AppID), InstanceID: mustPgUUID(p.InstanceID),
		WorkloadName: p.WorkloadName, Generation: p.Generation, Active: active, StartedAt: started})
	if err != nil {
		return mapErr(err)
	}
	_, err = q.ClearAppSecretRuntimeProcessAck(ctx, tx, sqlc.ClearAppSecretRuntimeProcessAckParams{AppID: mustPgUUID(p.AppID), InstanceID: mustPgUUID(p.InstanceID), WorkloadName: p.WorkloadName})
	if err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func lockSecretRuntimeApp(ctx context.Context, tx pgx.Tx, accountID, appID string) error {
	// Lock before observation rows: their promotion trigger takes this same lock.
	_, err := sqlc.New().LockBindingPromotionRevision(ctx, tx, sqlc.LockBindingPromotionRevisionParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock secret runtime app: %w", mapErr(err))
	}
	return nil
}
