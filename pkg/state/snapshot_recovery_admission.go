package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SnapshotRecoveryAdmission asks the instance admission insert made with a
// context carrying it to bind the new instance's snapshot-recovery lineage
// (ADR-1523) in the insert's own transaction, instead of a transaction of
// its own after it (ADR-1741). A binding the insert cannot make refuses the
// whole admission with ErrConflict, so no instance exists unbound.
type SnapshotRecoveryAdmission struct {
	// SnapshotID is the snapshot the instance restores from, or "" for a
	// cold boot.
	SnapshotID string
	bound      bool
}

// Bound reports whether the last admission made with the request committed
// with its binding. A store that does not bind at admission leaves it
// false, and the caller binds separately.
func (r *SnapshotRecoveryAdmission) Bound() bool { return r != nil && r.bound }

type snapshotRecoveryAdmissionKey struct{}

// WithSnapshotRecoveryAdmission attaches r to ctx for the admission insert.
func WithSnapshotRecoveryAdmission(ctx context.Context, r *SnapshotRecoveryAdmission) context.Context {
	return context.WithValue(ctx, snapshotRecoveryAdmissionKey{}, r)
}

func snapshotRecoveryAdmissionFrom(ctx context.Context) (*SnapshotRecoveryAdmission, bool) {
	r, ok := ctx.Value(snapshotRecoveryAdmissionKey{}).(*SnapshotRecoveryAdmission)
	return r, ok && r != nil
}

// bindSnapshotRecoveryAtAdmissionDB binds inst, inserted in tx, at its
// deployment's current recovery generation. The deployment and app rows are
// share-locked: concurrent admissions do not wait on each other, and an
// operator cold boot, which advances the generation with an UPDATE, is
// ordered entirely before or after this admission.
func bindSnapshotRecoveryAtAdmissionDB(ctx context.Context, tx pgx.Tx, inst Instance, snapshotID string) error {
	if snapshotID != "" {
		if err := validateSnapshotRecoveryIDs(snapshotID); err != nil {
			return err
		}
	}
	bound, err := sqlc.New().BindAdmittedInstanceSnapshotRecovery(ctx, tx, sqlc.BindAdmittedInstanceSnapshotRecoveryParams{
		InstanceID: mustPgUUID(inst.ID), WakeID: mustPgUUID(inst.WakeID), DeploymentID: mustPgUUID(inst.DeploymentID),
		SnapshotID: mustPgUUID(snapshotID),
	})
	if err != nil {
		return fmt.Errorf("state: bind snapshot recovery at admission: %w", mapErr(err))
	}
	if bound != 1 {
		return ErrConflict
	}
	return nil
}

// admissionSnapshotRecoveryGenerationLocked is the MemStore mirror of the
// binding's checks; the caller holds m.mu.
func (m *MemStore) admissionSnapshotRecoveryGenerationLocked(deploymentID, snapshotID string) (int64, error) {
	generation := m.snapshotRecoveryGenerations[deploymentID]
	if snapshotID == "" {
		return generation, nil
	}
	for _, snap := range m.snapshots {
		if snap.ID == snapshotID && snap.DeploymentID == deploymentID && !snap.Stale && !snap.DeletePending && snap.RecoveryGeneration == generation {
			return generation, nil
		}
	}
	return 0, ErrConflict
}

func (m *MemStore) recordAdmissionSnapshotRecoveryLocked(ins Instance, snapshotID string, generation int64) {
	if m.instanceSnapshotRecovery == nil {
		m.instanceSnapshotRecovery = make(map[string]instanceSnapshotRecovery)
	}
	m.instanceSnapshotRecovery[ins.ID] = instanceSnapshotRecovery{WakeID: ins.WakeID, SnapshotID: snapshotID, Generation: generation}
}
