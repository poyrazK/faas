package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ MigrationSourceStore = (*PgStore)(nil)

func (s *PgStore) RecordMigrationSourceSnapshot(ctx context.Context, lease, node, memory, vmstate string) error {
	if err := validateSnapshotRecoveryIDs(node); err != nil {
		return err
	}
	if memory == "" || vmstate == "" {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().RecordMigrationSourceSnapshot(ctx, s.pool, sqlc.RecordMigrationSourceSnapshotParams{LeaseToken: lease, NodeID: mustPgUUID(node), MemoryKey: memory, StateKey: vmstate})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func migrationSourceFromSQL(r sqlc.MigrationSource) MigrationSource {
	return MigrationSource{MigrationSourceBinding: MigrationSourceBinding{InstanceID: pgUUIDString(r.InstanceID), SourceNodeID: pgUUIDString(r.SourceNodeID), SourceWakeID: pgUUIDString(r.SourceWakeID), LeaseToken: r.LeaseToken, NativeGeneration: pgUUIDString(r.NativeGeneration), PhysicalBinding: r.PhysicalBinding}, RAMMB: int(r.RamMb), CPUMillicores: int(r.CpuMillicores), VCPU: int(r.Vcpu), Phase: r.Phase, Proof: r.Proof}
}

func (s *PgStore) MigrationSourceByLease(ctx context.Context, lease string) (MigrationSource, error) {
	r, err := sqlc.New().ReadMigrationSource(ctx, s.pool, lease)
	return migrationSourceFromSQL(r), mapErr(err)
}

func (s *PgStore) BindMigrationSource(ctx context.Context, b MigrationSourceBinding) (MigrationSource, error) {
	if err := b.Validate(); err != nil {
		return MigrationSource{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MigrationSource{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockNodeAdmissionPolicy(ctx, tx); err != nil {
		return MigrationSource{}, err
	}
	if r, err := q.ReadMigrationSource(ctx, tx, b.LeaseToken); err == nil {
		source := migrationSourceFromSQL(r)
		if source.MigrationSourceBinding != b {
			return MigrationSource{}, ErrConflict
		}
		return source, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return MigrationSource{}, mapErr(err)
	}
	r, err := q.InsertMigrationSource(ctx, tx, sqlc.InsertMigrationSourceParams{LeaseToken: b.LeaseToken, InstanceID: mustPgUUID(b.InstanceID), NodeID: mustPgUUID(b.SourceNodeID), WakeID: mustPgUUID(b.SourceWakeID), Generation: mustPgUUID(b.NativeGeneration), Binding: b.PhysicalBinding})
	if err != nil {
		return MigrationSource{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationSource{}, mapErr(err)
	}
	return migrationSourceFromSQL(r), nil
}

func (s *PgStore) migrationSourceTx(ctx context.Context, lease string, fn func(*sqlc.Queries, pgx.Tx, *MigrationSource) error) (MigrationSource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MigrationSource{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockNodeAdmissionPolicy(ctx, tx); err != nil {
		return MigrationSource{}, err
	}
	r, err := q.LockMigrationSource(ctx, tx, lease)
	if err != nil {
		return MigrationSource{}, mapErr(err)
	}
	source := migrationSourceFromSQL(r)
	if err := fn(q, tx, &source); err != nil {
		return MigrationSource{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationSource{}, mapErr(err)
	}
	return source, nil
}

func (s *PgStore) RequestMigrationSourceRetirement(ctx context.Context, lease, node string) (MigrationSource, error) {
	return s.migrationSourceTx(ctx, lease, func(q *sqlc.Queries, tx pgx.Tx, r *MigrationSource) error {
		if r.SourceNodeID != node || r.Phase == MigrationSourceResumed {
			return ErrConflict
		}
		if r.Phase != MigrationSourceBound {
			return nil
		}
		count, err := q.SetMigrationSourceRetiring(ctx, tx, lease)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		r.Phase = MigrationSourceRetiring
		return nil
	})
}

func (s *PgStore) AcknowledgeMigrationSource(ctx context.Context, b MigrationSourceBinding, outcome, proof string) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if proof == "" || len(proof) > api.MigrationCleanupProofMaxBytes || outcome != MigrationSourceRetired && outcome != MigrationSourceResumed {
		return ErrInvalidArgument
	}
	_, err := s.migrationSourceTx(ctx, b.LeaseToken, func(q *sqlc.Queries, tx pgx.Tx, r *MigrationSource) error {
		if r.MigrationSourceBinding != b {
			return ErrConflict
		}
		if !r.Pending() {
			if r.Phase != outcome || r.Proof != proof {
				return ErrConflict
			}
			return nil
		}
		count, err := q.AcknowledgeMigrationSource(ctx, tx, sqlc.AcknowledgeMigrationSourceParams{LeaseToken: b.LeaseToken, Phase: outcome, Proof: proof})
		if err == nil && count != 1 {
			return ErrConflict
		}
		return err
	})
	return err
}

func (s *PgStore) ListMigrationSourcesForRecovery(ctx context.Context, node string, now time.Time, after string, limit int) ([]MigrationSource, error) {
	if err := validateSnapshotRecoveryIDs(node); err != nil || limit < 1 || limit > api.MigrationAdmissionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListMigrationSourcesForRecovery(ctx, s.pool, sqlc.ListMigrationSourcesForRecoveryParams{NodeID: mustPgUUID(node), NowAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, AfterToken: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationSource, 0, len(rows))
	for _, r := range rows {
		out = append(out, migrationSourceFromSQL(r))
	}
	return out, nil
}

func (s *PgStore) ListMigrationSourceLeasesForRecovery(ctx context.Context, node string, now time.Time, after string, limit int) ([]MigrationLease, error) {
	if err := validateSnapshotRecoveryIDs(node); err != nil || limit < 1 || limit > api.MigrationAdmissionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListMigrationSourceLeasesForRecovery(ctx, s.pool, sqlc.ListMigrationSourceLeasesForRecoveryParams{NodeID: mustPgUUID(node), NowAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, AfterToken: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationLease, 0, len(rows))
	for _, r := range rows {
		out = append(out, MigrationLease{InstanceID: pgUUIDString(r.InstanceID), LeaseToken: r.LeaseToken, SourceNodeID: r.SourceNodeID, CreatedAt: r.CreatedAt.Time.UTC(), LeaseExpiresAt: r.LeaseExpiresAt.Time.UTC(), MemStorageKey: r.MemStorageKey, VMStateStorageKey: r.VmstateStorageKey, Pending: r.Pending})
	}
	return out, nil
}
