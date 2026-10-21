package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ClaimNextProjectEnvironmentPromotionHealth(ctx context.Context, token string, preferRecovery bool) (*PromotionHealthJob, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockPromotionHealthDispatch(ctx, tx); err != nil {
		return nil, mapErr(err)
	}
	// The claim statement takes a fresh READ COMMITTED snapshot after the dispatch
	// lock. This prevents concurrent dispatchers from oversubscribing shared slots.
	r, err := q.ClaimNextPromotionHealth(ctx, tx, sqlc.ClaimNextPromotionHealthParams{LeaseToken: token, PreferRecovery: preferRecovery, Concurrency: api.ProjectPromotionHealthConcurrency, AccountConcurrency: api.ProjectPromotionHealthAccountConcurrency, LeaseSeconds: api.ProjectPromotionHealthLeaseDuration.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &PromotionHealthJob{r.PromotionID, r.AccountID, r.ProjectSlug, r.ToEnvironment}, nil
}

func (s *PgStore) ProjectEnvironmentPromotionHealthLeaseCurrent(ctx context.Context, account, id, token string) (bool, error) {
	current, err := sqlc.New().PromotionHealthLeaseCurrent(ctx, s.pool, sqlc.PromotionHealthLeaseCurrentParams{AccountID: account, PromotionID: id, LeaseToken: token})
	return current, mapErr(err)
}

func (s *PgStore) finishPromotionHealth(ctx context.Context, id, token string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := q.LockPromotionHealthSchedule(ctx, tx, sqlc.LockPromotionHealthScheduleParams{PromotionID: id, LeaseToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	var h api.ProjectPromotionHealth
	if err = json.Unmarshal(r.Payload, &h); err != nil {
		return err
	}
	recovery, err := readPromotionRecoveryDB(ctx, tx, r.AccountID, id, "")
	if err != nil {
		return err
	}
	var receipt *api.ProjectPromotionMigrationRecovery
	if recovery != nil {
		receipt = copyPromotionRecovery(*recovery)
	}
	next := nextPromotionHealthCheck(&h, receipt, time.Now().UTC())
	if err = q.ReleasePromotionHealth(ctx, tx, sqlc.ReleasePromotionHealthParams{PromotionID: id, LeaseToken: token, NextCheckAt: pgtype.Timestamptz{Time: next, Valid: true}}); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) claimPromotionHealth(ctx context.Context, account, id, token string) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockPromotionHealthDispatch(ctx, tx); err != nil {
		return false, mapErr(err)
	}
	available, err := q.PromotionHealthSlotsAvailable(ctx, tx, sqlc.PromotionHealthSlotsAvailableParams{AccountID: account, Concurrency: api.ProjectPromotionHealthConcurrency, AccountConcurrency: api.ProjectPromotionHealthAccountConcurrency})
	if err != nil {
		return false, mapErr(err)
	}
	if !available.Valid || !available.Bool {
		return false, nil
	}
	n, err := q.ClaimPromotionHealth(ctx, tx, sqlc.ClaimPromotionHealthParams{AccountID: account, PromotionID: id, LeaseToken: token, LeaseSeconds: api.ProjectPromotionHealthLeaseDuration.Seconds()})
	if err != nil {
		return false, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return n == 1, nil
}
