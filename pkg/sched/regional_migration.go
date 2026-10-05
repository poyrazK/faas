package sched

// adr: 628
import (
	"bytes"
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// These explicit capabilities do not issue seats or activate migration. The
// scheduler must first verify native source pause and durably issue rollback.
type RegionalMigrationVMM interface {
	PrepareRegionalLiveMigration(context.Context, string, string, string, runtimequota.Placement) (LiveMigrationPrepare, error)
	CancelRegionalLiveMigration(context.Context, string, string, string, runtimequota.Placement) error
}

func (c *VMMClient) PrepareRegionalLiveMigration(ctx context.Context, instance, snapshotKey string, p runtimequota.Placement) (LiveMigrationPrepare, error) {
	raw, err := regionalResumeWire(instance, p)
	if err != nil {
		return LiveMigrationPrepare{}, err
	}
	resp, err := c.cli.PrepareLiveMigration(ctx, &vmmdpb.PrepareLiveMigrationRequest{InstanceId: instance, SnapshotStorageKey: snapshotKey, RegionalRuntime: raw})
	if err != nil {
		return LiveMigrationPrepare{}, liftErr(err)
	}
	if resp.GetLeaseToken() == "" || resp.GetMemStorageKey() != snapshotKey || resp.GetVmstateStorageKey() == "" || !bytes.Equal(resp.GetRegionalRuntime(), raw) {
		return LiveMigrationPrepare{}, runtimequota.ErrTrust
	}
	return LiveMigrationPrepare{MemStorageKey: resp.GetMemStorageKey(), VMStateStorageKey: resp.GetVmstateStorageKey(), LeaseToken: resp.GetLeaseToken(), FCVersion: resp.GetFcVersion()}, nil
}

func (c *VMMClient) CancelRegionalLiveMigration(ctx context.Context, instance, token string, p runtimequota.Placement) error {
	raw, err := regionalResumeWire(instance, p)
	if err != nil {
		return err
	}
	if token == "" {
		return runtimequota.ErrBinding
	}
	resp, err := c.cli.CancelLiveMigration(ctx, &vmmdpb.CancelLiveMigrationRequest{InstanceId: instance, LeaseToken: token, RegionalRuntime: raw})
	if err != nil {
		return liftErr(err)
	}
	if resp.GetInstanceId() != instance || resp.GetLeaseToken() != token || !bytes.Equal(resp.GetRegionalRuntime(), raw) {
		return runtimequota.ErrTrust
	}
	return nil
}

func (r *VMMRouter) PrepareRegionalLiveMigration(ctx context.Context, node, instance, key string, p runtimequota.Placement) (LiveMigrationPrepare, error) {
	if _, err := regionalResumeWire(instance, p); err != nil {
		return LiveMigrationPrepare{}, err
	}
	if !runtimequota.MatchesID(p.Permit.NodeID, node) {
		return LiveMigrationPrepare{}, runtimequota.ErrBinding
	}
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return LiveMigrationPrepare{}, err
	}
	preparer, ok := cli.(interface {
		PrepareRegionalLiveMigration(context.Context, string, string, runtimequota.Placement) (LiveMigrationPrepare, error)
	})
	if !ok {
		return LiveMigrationPrepare{}, api.NewProblem(501, api.CodeNotImplemented, "Regional migration unavailable", "vmmd client does not support source-bound capture")
	}
	return preparer.PrepareRegionalLiveMigration(ctx, instance, key, p.Clone())
}

func (r *VMMRouter) CancelRegionalLiveMigration(ctx context.Context, node, instance, token string, p runtimequota.Placement) error {
	if _, err := regionalResumeWire(instance, p); err != nil {
		return err
	}
	if !runtimequota.MatchesID(p.Permit.NodeID, node) {
		return runtimequota.ErrBinding
	}
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return err
	}
	resumer, ok := cli.(interface {
		CancelRegionalLiveMigration(context.Context, string, string, runtimequota.Placement) error
	})
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Regional migration unavailable", "vmmd client does not support lease-bound rollback")
	}
	return resumer.CancelRegionalLiveMigration(ctx, instance, token, p.Clone())
}
