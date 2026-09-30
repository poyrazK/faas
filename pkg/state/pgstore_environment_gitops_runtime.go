package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsRuntimeStore = (*PgStore)(nil)

func readGitOpsRuntime(ctx context.Context, tx sqlc.DBTX, source EnvironmentGitSource) ([]EnvironmentGitOpsRuntimeTarget, error) {
	rows, err := sqlc.New().ObserveEnvironmentGitOpsRuntime(ctx, tx, sqlc.ObserveEnvironmentGitOpsRuntimeParams{
		SourceID: mustPgUUID(source.ID), AccountID: mustPgUUID(source.AccountID)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentGitOpsRuntimeTarget, 0, len(rows))
	for _, row := range rows {
		out = append(out, EnvironmentGitOpsRuntimeTarget{AppID: pgUUIDString(row.AppID), Resource: row.Resource,
			Environment: row.EnvironmentSlug, RequiredAt: row.RequiredAt.Time, StaleResidents: row.StaleResidents,
			StartingResidents: row.StartingResidents, StaleSnapshots: row.StaleSnapshots})
	}
	return out, nil
}

func (s *PgStore) ObserveEnvironmentGitOpsRuntime(ctx context.Context, lease EnvironmentGitOpsLease) ([]EnvironmentGitOpsRuntimeTarget, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return readGitOpsRuntime(ctx, tx, lease.Source)
}

func insertGitOpsRuntimeEffect(ctx context.Context, tx sqlc.DBTX, lease EnvironmentGitOpsLease, plan environmentsync.Plan, appID string, requiredAt time.Time) error {
	_, err := sqlc.New().InsertEnvironmentGitOpsRuntimeEffect(ctx, tx, sqlc.InsertEnvironmentGitOpsRuntimeEffectParams{
		SourceID: mustPgUUID(lease.Source.ID), RevisionID: mustPgUUID(lease.Revision.ID), PlanHash: plan.Hash,
		AppID: mustPgUUID(appID), RequiredAt: gitOpsTime(requiredAt)})
	return mapErr(err)
}

func (s *PgStore) EnsureEnvironmentGitOpsRuntime(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) error {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if lease.Source.Spec.Mode != "enforce" {
		return ErrConflict
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return err
	}
	observed, _, err := readEnvironmentGitOpsIntent(ctx, tx, lease.Source, desired)
	if err != nil {
		return err
	}
	plan, err := environmentGitOpsPlan(lease.Source, lease.Revision, desired, observed, false)
	if err != nil {
		return err
	}
	if !plan.CanApply() || plan.HasDrift() || plan.Hash != reviewed.Hash {
		return ErrConflict
	}
	targets, err := readGitOpsRuntime(ctx, tx, lease.Source)
	if err != nil {
		return err
	}
	pending, err := sqlc.New().PendingEnvironmentGitOpsRuntime(ctx, tx, mustPgUUID(lease.Source.ID))
	if err != nil {
		return mapErr(err)
	}
	appsPending := map[string]bool{}
	for _, effect := range pending {
		appsPending[pgUUIDString(effect.AppID)] = true
	}
	for _, target := range targets {
		if !target.Ready() && !appsPending[target.AppID] {
			if err := insertGitOpsRuntimeEffect(ctx, tx, lease, plan, target.AppID, target.RequiredAt); err != nil {
				return err
			}
		}
	}
	return mapErr(tx.Commit(ctx))
}

func runtimeGitOpsEffectFromSQL(row sqlc.EnvironmentGitopsRuntimeEffect) EnvironmentGitOpsRuntimeEffect {
	effect := EnvironmentGitOpsRuntimeEffect{ID: pgUUIDString(row.ID), SourceID: pgUUIDString(row.SourceID),
		Generation: row.Generation, PlanHash: row.PlanHash,
		AppID: pgUUIDString(row.AppID), Environment: row.EnvironmentSlug, RequiredAt: row.RequiredAt.Time,
		WakeID: pgUUIDString(row.WakeID), NextRequestAt: row.NextRequestAt.Time}
	if row.RequestedAt.Valid {
		at := row.RequestedAt.Time
		effect.RequestedAt = &at
	}
	if row.CompletedAt.Valid {
		at := row.CompletedAt.Time
		effect.CompletedAt = &at
	}
	return effect
}

func (s *PgStore) PendingEnvironmentGitOpsRuntime(ctx context.Context, lease EnvironmentGitOpsLease) ([]EnvironmentGitOpsRuntimeEffect, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := sqlc.New().PendingEnvironmentGitOpsRuntime(ctx, tx, mustPgUUID(lease.Source.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentGitOpsRuntimeEffect, 0, len(rows))
	for _, row := range rows {
		out = append(out, runtimeGitOpsEffectFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) ReconcileEnvironmentGitOpsRuntime(ctx context.Context, lease EnvironmentGitOpsLease, id string) (EnvironmentGitOpsRuntimeProgress, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return EnvironmentGitOpsRuntimeProgress{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	effect, err := q.LockEnvironmentGitOpsRuntimeEffect(ctx, tx, sqlc.LockEnvironmentGitOpsRuntimeEffectParams{SourceID: mustPgUUID(lease.Source.ID), EffectID: mustPgUUID(id)})
	if err != nil {
		return EnvironmentGitOpsRuntimeProgress{}, mapErr(err)
	}
	if err := q.InvalidateEnvironmentGitOpsRuntimeAtBoundary(ctx, tx, sqlc.InvalidateEnvironmentGitOpsRuntimeAtBoundaryParams{AppID: effect.AppID, RequiredAt: effect.RequiredAt}); err != nil {
		return EnvironmentGitOpsRuntimeProgress{}, mapErr(err)
	}
	targets, err := readGitOpsRuntime(ctx, tx, lease.Source)
	if err != nil {
		return EnvironmentGitOpsRuntimeProgress{}, err
	}
	var target *EnvironmentGitOpsRuntimeTarget
	for i := range targets {
		if targets[i].AppID == pgUUIDString(effect.AppID) {
			target = &targets[i]
			break
		}
	}
	if target == nil {
		return EnvironmentGitOpsRuntimeProgress{}, ErrConflict
	}
	if target.RequiredAt.After(effect.RequiredAt.Time) {
		effect, err = q.AdvanceEnvironmentGitOpsRuntimeBoundary(ctx, tx, sqlc.AdvanceEnvironmentGitOpsRuntimeBoundaryParams{
			SourceID: effect.SourceID, EffectID: effect.ID, RequiredAt: gitOpsTime(target.RequiredAt)})
		if err != nil {
			return EnvironmentGitOpsRuntimeProgress{}, mapErr(err)
		}
		// Publish the advanced boundary before handing off its new wake ID.
		// Otherwise schedd could consider an older refresh sufficient and
		// suppress every replay of this request as already completed.
		if err := q.InvalidateEnvironmentGitOpsRuntimeAtBoundary(ctx, tx, sqlc.InvalidateEnvironmentGitOpsRuntimeAtBoundaryParams{AppID: effect.AppID, RequiredAt: effect.RequiredAt}); err != nil {
			return EnvironmentGitOpsRuntimeProgress{}, mapErr(err)
		}
		targets, err = readGitOpsRuntime(ctx, tx, lease.Source)
		if err != nil {
			return EnvironmentGitOpsRuntimeProgress{}, err
		}
		for i := range targets {
			if targets[i].AppID == pgUUIDString(effect.AppID) {
				target = &targets[i]
				break
			}
		}
	}
	if target.Ready() {
		count, err := q.CompleteEnvironmentGitOpsRuntime(ctx, tx, sqlc.CompleteEnvironmentGitOpsRuntimeParams{SourceID: effect.SourceID, EffectID: effect.ID})
		if err != nil {
			return EnvironmentGitOpsRuntimeProgress{}, mapErr(err)
		}
		if count != 1 {
			return EnvironmentGitOpsRuntimeProgress{}, ErrConflict
		}
	} else if lease.Source.Spec.Mode == "enforce" && target.StaleResidents > 0 && !effect.NextRequestAt.Time.After(time.Now()) {
		if err := requestGitOpsRuntimeTx(ctx, tx, effect); err != nil {
			return EnvironmentGitOpsRuntimeProgress{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentGitOpsRuntimeProgress{}, mapErr(err)
	}
	return EnvironmentGitOpsRuntimeProgress{Ready: target.Ready()}, nil
}

func requestGitOpsRuntimeTx(ctx context.Context, tx pgx.Tx, effect sqlc.EnvironmentGitopsRuntimeEffect) error {
	raw, _ := json.Marshal(EnvironmentGitOpsRuntimeRequest{AppID: pgUUIDString(effect.AppID), WakeID: pgUUIDString(effect.WakeID), Scope: effect.EnvironmentSlug})
	if err := db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyRuntimeConfigRestart, string(raw)); err != nil {
		return err
	}
	return sqlc.New().RequestEnvironmentGitOpsRuntimeRefresh(ctx, tx, sqlc.RequestEnvironmentGitOpsRuntimeRefreshParams{
		SourceID: effect.SourceID, EffectID: effect.ID, NextRequestAt: gitOpsTime(time.Now().Add(api.EnvironmentGitOpsRuntimeRefreshRetry))})
}
