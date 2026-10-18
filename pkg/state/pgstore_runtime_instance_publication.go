package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The original environment, app, deployment, instance and retained pins stay
// locked through the runtime/state CAS. Deletion cannot slip between the
// ownership check and publishing a paused or routable instance. A busy
// application-standard fence replays the whole transaction: the VM has
// already booted, and discarding it for a millisecond collision wastes it.
func (s *PgStore) PublishOwnedInstanceRuntime(ctx context.Context, p RuntimeInstancePublication) (Instance, error) {
	if err := validateRuntimeInstancePublication(p); err != nil {
		return Instance{}, err
	}
	return retryRuntimeAdmissionBusy(ctx, func() (Instance, error) {
		return s.publishOwnedInstanceRuntimeOnce(ctx, p)
	})
}

// RuntimePublicationOutcomeUnknown reports whether a failed publication may
// still have committed. A refusal from the database or the store rolled the
// whole transaction back, so only a deadline or transport failure can hide a
// commit whose acknowledgment was lost.
func RuntimePublicationOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return false
	}
	for _, refused := range []error{ErrConflict, ErrNotFound, ErrInvalidArgument, ErrApplicationStandardRuntimeStale,
		ErrApplicationStandardsPending, ErrApplicationStandardRuntimeBusy} {
		if errors.Is(err, refused) {
			return false
		}
	}
	return true
}

func (s *PgStore) publishOwnedInstanceRuntimeOnce(ctx context.Context, p RuntimeInstancePublication) (Instance, error) {
	guard, guarded, err := generationGuardedPublication(ctx, p)
	if err != nil {
		return Instance{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !guarded {
		if err := runtimeAppConfigFenceDB(ctx, tx, p); err != nil {
			return Instance{}, err
		}
	}
	if p.ExpectedState == string(StateWarm) || p.ExpectedState == string(StateFrozen) {
		fresh, err := sqlc.New().InstanceSnapshotRecoveryFresh(ctx, tx, mustPgUUID(p.InstanceID))
		if err != nil {
			return Instance{}, mapErr(err)
		}
		if !fresh {
			return Instance{}, ErrConflict
		}
	}
	acceptance, err := prepareRuntimeUpgradeAcceptanceDB(ctx, tx, p)
	if err != nil {
		return Instance{}, err
	}
	instance, err := publishOwnedRuntimeRow(ctx, tx, p)
	if err != nil {
		return Instance{}, err
	}
	saved, err := sqlc.New().SaveRuntimeInstanceConfigProof(ctx, tx, runtimeConfigProofParams(p))
	if err != nil {
		return Instance{}, err
	}
	if p.Inputs != nil && p.targetState() == string(StateRunning) {
		if err := recordInstanceRuntimeConfigReceipt(ctx, tx, p.InstanceID, p.WakeID, *p.Inputs); err != nil {
			return Instance{}, err
		}
	}
	if acceptance != nil {
		if err := insertRuntimeUpgradeAcceptanceDB(ctx, tx, *acceptance); err != nil {
			return Instance{}, err
		}
	}
	// ADR-1741: the generation stands in for the fence's reads. It is taken
	// last, after the row locks above, so it cannot close a lock cycle with
	// a writer that bumps it after its own row locks.
	if guarded {
		if err := checkRuntimeGenerationTx(ctx, tx, guard); err != nil {
			return Instance{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	if p.Bookkeeping != nil {
		p.Bookkeeping.SnapshotBackoffRecorded = saved.SnapshotBackoffRecorded
	}
	return instance, nil
}

// runtimeConfigProofParams saves the publication's config proof and, in the
// same statement, its bookkeeping.
func runtimeConfigProofParams(p RuntimeInstancePublication) sqlc.SaveRuntimeInstanceConfigProofParams {
	params := sqlc.SaveRuntimeInstanceConfigProofParams{
		InstanceID: mustPgUUID(p.InstanceID), EnvironmentID: mustPgUUID(p.Fence.EnvironmentID), Scope: p.Fence.Scope,
		SecretFingerprint: p.Fence.Fingerprint, ConfigFingerprint: p.ConfigFence.Fingerprint,
	}
	if b := p.Bookkeeping; b != nil {
		if b.EventKind != "" {
			params.EventKind = pgtype.Text{String: b.EventKind, Valid: true}
			params.EventData = b.EventData
		}
		if b.NotifyChannel != "" {
			params.NotifyChannel = pgtype.Text{String: b.NotifyChannel, Valid: true}
			params.NotifyPayload = pgtype.Text{String: b.NotifyPayload, Valid: true}
		}
	}
	return params
}

func (s *PgStore) InstanceRuntimeConfigFence(ctx context.Context, accountID, appID, instanceID string) (RuntimeAppConfigFence, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, instanceID); err != nil {
		return RuntimeAppConfigFence{}, err
	}
	row, err := sqlc.New().ReadRuntimeInstanceConfigProof(ctx, s.pool, sqlc.ReadRuntimeInstanceConfigProofParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), InstanceID: mustPgUUID(instanceID),
	})
	if err != nil {
		return RuntimeAppConfigFence{}, mapErr(err)
	}
	return RuntimeAppConfigFence{SecretFence: RuntimeAppSecretFence{DeploymentID: row.DeploymentID, EnvironmentID: row.EnvironmentID,
		Scope: row.Scope, Fingerprint: row.SecretFingerprint}, Fingerprint: row.ConfigFingerprint}, nil
}

// Writers serialize on app/deployment parents. Avoid child-row locks here:
// UPDATE can already own a tuple while its BEFORE trigger waits on the parent.
func runtimeAppConfigFenceDB(ctx context.Context, db sqlc.DBTX, p RuntimeInstancePublication) error {
	q, fence := sqlc.New(), p.Fence
	if fence.EnvironmentID != "" {
		if _, err := q.LockRuntimeSecretEnvironment(ctx, db, sqlc.LockRuntimeSecretEnvironmentParams{
			EnvironmentID: mustPgUUID(fence.EnvironmentID), AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(p.AppID), Scope: fence.Scope,
		}); err != nil {
			return runtimeSecretFenceError(err)
		}
	}
	if _, err := q.LockRuntimeSecretApp(ctx, db, sqlc.LockRuntimeSecretAppParams{AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(p.AppID)}); err != nil {
		return runtimeSecretFenceError(err)
	}
	owner, err := q.LockRuntimeSecretOwner(ctx, db, sqlc.LockRuntimeSecretOwnerParams{
		AccountID: mustPgUUID(p.AccountID), AppID: mustPgUUID(p.AppID), InstanceID: mustPgUUID(p.InstanceID), RequireActive: true,
	})
	if err != nil {
		return runtimeSecretFenceError(err)
	}
	if pgUUIDString(owner.DeploymentID) != fence.DeploymentID || owner.Scope != fence.Scope {
		return ErrConflict
	}
	if _, err := q.LockRuntimeConfigWorkloadSpec(ctx, db, owner.DeploymentID); err != nil {
		return runtimeSecretFenceError(err)
	}
	snapshot, err := runtimeAppValuesDB(ctx, db, p.AccountID, p.AppID, fence.DeploymentID)
	if err != nil {
		return runtimeSecretFenceError(err)
	}
	current, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || current != p.ConfigFence || current.SecretFence != fence {
		return ErrConflict
	}
	return nil
}

func instanceFromRuntimePublication(row sqlc.PublishOwnedInstanceRuntimeRow) Instance {
	instance := Instance{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID,
		State: row.State, Netns: row.Netns, GuestUID: int(row.GuestUid), HostIP: row.HostIp,
		RAMMB: int(row.RamMb), NodeID: row.NodeID, WakeID: row.WakeID, TailCount: int(row.TailCount),
		Mode: row.Mode, RequestCount: row.RequestCount}
	if row.StartedAt.Valid {
		instance.StartedAt = row.StartedAt.Time
	}
	if row.LastRequestAt.Valid {
		instance.LastRequestAt = row.LastRequestAt.Time
	}
	if row.ParkedAt.Valid {
		instance.ParkedAt = row.ParkedAt.Time
	}
	if row.FrameworkReadyAt.Valid {
		stamp := row.FrameworkReadyAt.Time
		instance.FrameworkReadyAt = &stamp
	}
	return instance
}
