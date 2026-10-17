package state

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ MigrationAdmissionStore = (*PgStore)(nil)

func migrationAdmissionFromSQL(r sqlc.MigrationAdmission) MigrationAdmission {
	return MigrationAdmission{MigrationAdmissionPlan: MigrationAdmissionPlan{ID: pgUUIDString(r.ID), InstanceID: pgUUIDString(r.InstanceID), SourceNodeID: pgUUIDString(r.SourceNodeID), DestinationNodeID: pgUUIDString(r.DestinationNodeID), SourceWakeID: pgUUIDString(r.SourceWakeID), DestinationWakeID: pgUUIDString(r.DestinationWakeID), DestinationGeneration: r.DestinationGeneration, LeaseToken: r.LeaseToken, MemStorageKey: r.MemStorageKey, VMStateStorageKey: r.VmstateStorageKey, RequestDigest: r.RequestDigest, RAMMB: int(r.RamMb), CPUMillicores: int(r.CpuMillicores), VCPU: int(r.Vcpu), ExpiresAt: r.ExpiresAt.Time.UTC()}, Phase: r.Phase, AdoptionReceipt: bytes.Clone(r.AdoptionReceipt), CleanupProof: r.CleanupProof}
}

func (s *PgStore) MigrationAdmissionByID(ctx context.Context, id string) (MigrationAdmission, error) {
	if err := validateSnapshotRecoveryIDs(id); err != nil {
		return MigrationAdmission{}, err
	}
	r, err := sqlc.New().ReadMigrationAdmission(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return MigrationAdmission{}, mapErr(err)
	}
	return migrationAdmissionFromSQL(r), nil
}

func (s *PgStore) ReserveMigrationAdmission(ctx context.Context, p MigrationAdmissionPlan) (MigrationAdmission, error) {
	p = normalizeMigrationAdmissionPlan(p)
	if err := validateMigrationAdmissionPlan(p); err != nil {
		return MigrationAdmission{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MigrationAdmission{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	read := func() (MigrationAdmission, error) {
		r, err := q.ReadMigrationAdmission(ctx, tx, mustPgUUID(p.ID))
		if err != nil {
			return MigrationAdmission{}, err
		}
		a := migrationAdmissionFromSQL(r)
		if a.MigrationAdmissionPlan != p {
			return MigrationAdmission{}, ErrConflict
		}
		return a, nil
	}
	if a, err := read(); err == nil {
		return a, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return MigrationAdmission{}, mapErr(err)
	}
	ctx = WithNodeAdmission(ctx, NodeAdmission{NodeID: p.DestinationNodeID, Generation: p.DestinationGeneration})
	if err := checkNodeAdmissionDB(ctx, tx, p.DestinationNodeID); err != nil {
		return MigrationAdmission{}, err
	}
	if err := q.LockMigrationAdmissionNode(ctx, tx, p.DestinationNodeID); err != nil {
		return MigrationAdmission{}, err
	}
	if a, err := read(); err == nil {
		return a, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return MigrationAdmission{}, mapErr(err)
	}
	r, err := q.InsertMigrationAdmission(ctx, tx, sqlc.InsertMigrationAdmissionParams{ID: mustPgUUID(p.ID), InstanceID: mustPgUUID(p.InstanceID), SourceNodeID: mustPgUUID(p.SourceNodeID), DestinationNodeID: mustPgUUID(p.DestinationNodeID), SourceWakeID: mustPgUUID(p.SourceWakeID), DestinationWakeID: mustPgUUID(p.DestinationWakeID), DestinationGeneration: p.DestinationGeneration, LeaseToken: p.LeaseToken, MemStorageKey: p.MemStorageKey, VmstateStorageKey: p.VMStateStorageKey, RequestDigest: p.RequestDigest, RamMb: int32(p.RAMMB), CpuMillicores: int32(p.CPUMillicores), Vcpu: int32(p.VCPU), ExpiresAt: pgtype.Timestamptz{Time: p.ExpiresAt, Valid: true}})
	if err != nil {
		return MigrationAdmission{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationAdmission{}, mapErr(err)
	}
	return migrationAdmissionFromSQL(r), nil
}

// Policy precedes node/reservation/instance locks. A start reads immutable
// identity first so it can lock the node before the admission row.
func (s *PgStore) migrationAdmissionTx(ctx context.Context, id, node string, start bool, fn func(*sqlc.Queries, pgx.Tx, *MigrationAdmission) error) (MigrationAdmission, error) {
	if err := validateSnapshotRecoveryIDs(id, node); err != nil {
		return MigrationAdmission{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MigrationAdmission{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockNodeAdmissionPolicy(ctx, tx); err != nil {
		return MigrationAdmission{}, err
	}
	if start {
		r, err := q.ReadMigrationAdmission(ctx, tx, mustPgUUID(id))
		if err != nil {
			return MigrationAdmission{}, mapErr(err)
		}
		if pgUUIDString(r.DestinationNodeID) != node {
			return MigrationAdmission{}, ErrConflict
		}
		if r.Phase == MigrationReserved {
			if err := checkNodeAdmissionDB(WithNodeAdmission(ctx, NodeAdmission{NodeID: node, Generation: r.DestinationGeneration}), tx, node); err != nil {
				return MigrationAdmission{}, err
			}
		}
	}
	r, err := q.LockMigrationAdmission(ctx, tx, mustPgUUID(id))
	if err != nil {
		return MigrationAdmission{}, mapErr(err)
	}
	a := migrationAdmissionFromSQL(r)
	if a.DestinationNodeID != node {
		return MigrationAdmission{}, ErrConflict
	}
	if err := fn(q, tx, &a); err != nil {
		return cloneMigrationAdmission(a), mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationAdmission{}, mapErr(err)
	}
	return cloneMigrationAdmission(a), nil
}

func (s *PgStore) StartMigrationAdoption(ctx context.Context, id, node, digest string) (MigrationAdmission, bool, error) {
	execute := false
	a, err := s.migrationAdmissionTx(ctx, id, node, true, func(q *sqlc.Queries, tx pgx.Tx, a *MigrationAdmission) error {
		if a.RequestDigest != digest {
			return ErrConflict
		}
		switch a.Phase {
		case MigrationAdopted, MigrationCommitted:
			return nil
		case MigrationAdopting:
			return ErrMigrationAdoptionUnresolved
		case MigrationReserved:
		default:
			return ErrConflict
		}
		count, err := q.StartMigrationAdmission(ctx, tx, mustPgUUID(id))
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		a.Phase = MigrationAdopting
		execute = true
		return nil
	})
	return a, execute && err == nil, err
}

func (s *PgStore) AcknowledgeMigrationAdoption(ctx context.Context, id, node, digest string, receipt []byte) error {
	if len(receipt) == 0 || len(receipt) > api.MigrationAdoptionReceiptMaxBytes {
		return ErrInvalidArgument
	}
	_, err := s.migrationAdmissionTx(ctx, id, node, false, func(q *sqlc.Queries, tx pgx.Tx, a *MigrationAdmission) error {
		if a.RequestDigest != digest {
			return ErrConflict
		}
		if a.Phase == MigrationAdopted || a.Phase == MigrationCommitted {
			if bytes.Equal(a.AdoptionReceipt, receipt) {
				return nil
			}
			return ErrConflict
		}
		if a.Phase != MigrationAdopting {
			return ErrConflict
		}
		count, err := q.AcknowledgeMigrationAdoption(ctx, tx, sqlc.AcknowledgeMigrationAdoptionParams{ID: mustPgUUID(id), Receipt: receipt})
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		return nil
	})
	return err
}

func (s *PgStore) RequestMigrationAdmissionCleanup(ctx context.Context, id, node string) (MigrationAdmission, error) {
	return s.migrationAdmissionTx(ctx, id, node, false, func(q *sqlc.Queries, tx pgx.Tx, a *MigrationAdmission) error {
		var count int64
		var err error
		switch a.Phase {
		case MigrationReserved:
			count, err = q.SetMigrationAdmissionCleaned(ctx, tx, sqlc.SetMigrationAdmissionCleanedParams{ID: mustPgUUID(id), Proof: "no_dispatch"})
			a.Phase, a.CleanupProof = MigrationCleaned, "no_dispatch"
		case MigrationAdopting, MigrationAdopted:
			count, err = q.SetMigrationAdmissionCleaning(ctx, tx, mustPgUUID(id))
			a.Phase = MigrationCleaning
		default:
			return nil
		}
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *PgStore) AcknowledgeMigrationAdmissionCleanup(ctx context.Context, id, node, proof string) error {
	if proof == "" || len(proof) > api.MigrationCleanupProofMaxBytes {
		return ErrInvalidArgument
	}
	_, err := s.migrationAdmissionTx(ctx, id, node, false, func(q *sqlc.Queries, tx pgx.Tx, a *MigrationAdmission) error {
		if a.Phase == MigrationCleaned {
			if a.CleanupProof == proof {
				return nil
			}
			return ErrConflict
		}
		if a.Phase != MigrationCleaning {
			return ErrConflict
		}
		count, err := q.SetMigrationAdmissionCleaned(ctx, tx, sqlc.SetMigrationAdmissionCleanedParams{ID: mustPgUUID(id), Proof: proof})
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		return nil
	})
	return err
}

func (s *PgStore) ListMigrationAdmissionsForRecovery(ctx context.Context, node string, now time.Time, after string, limit int) ([]MigrationAdmission, error) {
	if limit < 1 || limit > api.MigrationAdmissionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	if err := validateSnapshotRecoveryIDs(node); err != nil {
		return nil, err
	}
	if after != "" {
		if err := validateSnapshotRecoveryIDs(after); err != nil {
			return nil, err
		}
	}
	rows, err := sqlc.New().ListMigrationAdmissionsForRecovery(ctx, s.pool, sqlc.ListMigrationAdmissionsForRecoveryParams{NodeID: mustPgUUID(node), NowAt: pgtype.Timestamptz{Time: now, Valid: true}, AfterID: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationAdmission, 0, len(rows))
	for _, r := range rows {
		out = append(out, migrationAdmissionFromSQL(r))
	}
	return out, nil
}

func (s *PgStore) FenceMigrationSourceResume(ctx context.Context, id, lease string) (bool, error) {
	if err := validateSnapshotRecoveryIDs(id); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockNodeAdmissionPolicy(ctx, tx); err != nil {
		return false, err
	}
	_, err = q.LockMigrationSourceResume(ctx, tx, sqlc.LockMigrationSourceResumeParams{InstanceID: mustPgUUID(id), LeaseToken: lease})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, mapErr(err)
	}
	safe, err := q.MigrationSourceMayResume(ctx, tx, sqlc.MigrationSourceMayResumeParams{InstanceID: mustPgUUID(id), LeaseToken: lease})
	if err != nil {
		return false, mapErr(err)
	}
	if !safe {
		return false, nil
	}
	if err := q.FenceMigrationSourceResume(ctx, tx, sqlc.FenceMigrationSourceResumeParams{InstanceID: mustPgUUID(id), LeaseToken: lease}); err != nil {
		return false, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, mapErr(err)
	}
	return true, nil
}

func lockMigrationAdmissionCommit(ctx context.Context, tx pgx.Tx, id, to string) error {
	admission := migrationAdmissionID(ctx)
	if admission == "" {
		return nil
	}
	if err := validateSnapshotRecoveryIDs(admission); err != nil {
		return err
	}
	r, err := sqlc.New().LockMigrationAdmission(ctx, tx, mustPgUUID(admission))
	if err != nil {
		return mapErr(err)
	}
	if pgUUIDString(r.InstanceID) != id || pgUUIDString(r.DestinationNodeID) != to || r.Phase != MigrationAdopted {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) migrationAdmissionResources(ctx context.Context, nodes []string) ([]sqlc.MigrationAdmissionNodeResourcesRow, error) {
	ids := make([]pgtype.UUID, 0, len(nodes))
	for _, id := range nodes {
		if err := validateSnapshotRecoveryIDs(id); err != nil {
			return nil, err
		}
		ids = append(ids, mustPgUUID(id))
	}
	rows, err := sqlc.New().MigrationAdmissionNodeResources(ctx, s.pool, ids)
	return rows, mapErr(err)
}
