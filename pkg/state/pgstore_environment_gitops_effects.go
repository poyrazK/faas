package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsEffectStore = (*PgStore)(nil)

func gitOpsEffectFromSQL(raw sqlc.EnvironmentGitopsEffect) EnvironmentGitOpsEffect {
	out := EnvironmentGitOpsEffect{
		EnvironmentGitOpsEffectSpec: EnvironmentGitOpsEffectSpec{AppID: pgUUIDString(raw.AppID), Kind: raw.Kind,
			GatewayGeneration: raw.GatewayGeneration, MatchHosts: raw.MatchHosts, ExpectedNodes: raw.ExpectedNodes},
		ID: pgUUIDString(raw.ID), SourceID: pgUUIDString(raw.SourceID), RevisionID: pgUUIDString(raw.RevisionID),
		Generation: raw.Generation, IntentVersion: raw.IntentVersion, PlanHash: raw.PlanHash, AcknowledgedNodes: raw.AcknowledgedNodes,
	}
	if raw.CompletedAt.Valid {
		completed := raw.CompletedAt.Time
		out.CompletedAt = &completed
	}
	return out
}

func (s *PgStore) gitOpsEffectTx(ctx context.Context, lease EnvironmentGitOpsLease) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func (s *PgStore) PendingEnvironmentGitOpsEffects(ctx context.Context, lease EnvironmentGitOpsLease) ([]EnvironmentGitOpsEffect, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := sqlc.New().PendingEnvironmentGitOpsEffects(ctx, tx, mustPgUUID(lease.Source.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentGitOpsEffect, 0, len(rows))
	for _, row := range rows {
		out = append(out, gitOpsEffectFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) ExtendEnvironmentGitOpsEffectTargets(ctx context.Context, lease EnvironmentGitOpsLease, id string, nodes []string) (EnvironmentGitOpsEffect, error) {
	nodes, err := canonicalGitOpsEffectNames(nodes)
	if err != nil {
		return EnvironmentGitOpsEffect{}, err
	}
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return EnvironmentGitOpsEffect{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().ExtendEnvironmentGitOpsEffectTargets(ctx, tx, sqlc.ExtendEnvironmentGitOpsEffectTargetsParams{
		SourceID: mustPgUUID(lease.Source.ID), EffectID: mustPgUUID(id), Nodes: nodes,
	})
	if err != nil {
		return EnvironmentGitOpsEffect{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentGitOpsEffect{}, mapErr(err)
	}
	return gitOpsEffectFromSQL(row), nil
}

func (s *PgStore) AcknowledgeEnvironmentGitOpsEffect(ctx context.Context, lease EnvironmentGitOpsLease, id string, generation int64, node string) error {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	count, err := sqlc.New().AcknowledgeEnvironmentGitOpsEffect(ctx, tx, sqlc.AcknowledgeEnvironmentGitOpsEffectParams{
		SourceID: mustPgUUID(lease.Source.ID), EffectID: mustPgUUID(id), GatewayGeneration: generation, Node: node,
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) CompleteEnvironmentGitOpsEffect(ctx context.Context, lease EnvironmentGitOpsLease, id string) error {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	count, err := sqlc.New().CompleteEnvironmentGitOpsEffect(ctx, tx, sqlc.CompleteEnvironmentGitOpsEffectParams{
		SourceID: mustPgUUID(lease.Source.ID), EffectID: mustPgUUID(id),
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
