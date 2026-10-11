package state

import (
	"context"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The original environment, app, deployment, instance and retained pins stay
// locked through the runtime/state CAS. Deletion cannot slip between the
// ownership check and publishing a paused or routable instance.
func (s *PgStore) PublishOwnedInstanceRuntime(ctx context.Context, p RuntimeInstancePublication) (Instance, error) {
	if err := validateRuntimeInstancePublication(p); err != nil {
		return Instance{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := runtimeAppConfigFenceDB(ctx, tx, p); err != nil {
		return Instance{}, err
	}
	acceptance, err := prepareRuntimeUpgradeAcceptanceDB(ctx, tx, p)
	if err != nil {
		return Instance{}, err
	}
	address, _ := netip.ParseAddr(p.HostIP) // validated before opening the transaction
	row, err := sqlc.New().PublishOwnedInstanceRuntime(ctx, tx, sqlc.PublishOwnedInstanceRuntimeParams{
		InstanceID: mustPgUUID(p.InstanceID), AppID: mustPgUUID(p.AppID), DeploymentID: mustPgUUID(p.Fence.DeploymentID),
		NodeID: mustPgUUID(p.NodeID), WakeID: mustPgUUID(p.WakeID), ExpectedState: p.ExpectedState, TargetState: p.targetState(),
		Netns: p.Netns, HostIp: address, GuestUid: int32(p.GuestUID),
		ConfigFingerprint: p.ConfigFence.Fingerprint,
	})
	if err != nil {
		return Instance{}, runtimeSecretFenceError(err)
	}
	if err := sqlc.New().SaveRuntimeInstanceConfigProof(ctx, tx, sqlc.SaveRuntimeInstanceConfigProofParams{
		InstanceID: mustPgUUID(p.InstanceID), EnvironmentID: mustPgUUID(p.Fence.EnvironmentID), Scope: p.Fence.Scope,
		SecretFingerprint: p.Fence.Fingerprint, ConfigFingerprint: p.ConfigFence.Fingerprint,
	}); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	return instanceFromRuntimePublication(row), nil
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
