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
	if _, err := runtimeAppSecretFenceDB(ctx, tx, p.AccountID, p.AppID, p.InstanceID, p.Fence, true); err != nil {
		return Instance{}, err
	}
	address, _ := netip.ParseAddr(p.HostIP) // validated before opening the transaction
	row, err := sqlc.New().PublishOwnedInstanceRuntime(ctx, tx, sqlc.PublishOwnedInstanceRuntimeParams{
		InstanceID: mustPgUUID(p.InstanceID), AppID: mustPgUUID(p.AppID), DeploymentID: mustPgUUID(p.Fence.DeploymentID),
		NodeID: mustPgUUID(p.NodeID), WakeID: mustPgUUID(p.WakeID), ExpectedState: p.ExpectedState, TargetState: p.targetState(),
		Netns: p.Netns, HostIp: address, GuestUid: int32(p.GuestUID),
	})
	if err != nil {
		return Instance{}, runtimeSecretFenceError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	return instanceFromRuntimePublication(row), nil
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
