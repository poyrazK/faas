package sched

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (c *VMMClient) CaptureAdmittedRuntime(ctx context.Context, g runtimeadmission.SnapshotGrant) (SnapshotBytes, runtimeadmission.SnapshotAcknowledgment, error) {
	g = g.Clone()
	if err := g.Validate(time.Now()); err != nil {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	p, err := c.cli.CaptureAdmittedRuntime(ctx, &vmmdpb.CaptureAdmittedRuntimeRequest{Grant: g.ToProto()})
	if err != nil {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, liftErr(err)
	}
	a, err := runtimeadmission.CheckAdmittedSnapshotResponse(p, g)
	if err != nil {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	b := SnapshotBytes{MemBytes: p.Snapshot.MemBytes, VMStateBytes: p.Snapshot.VmstateBytes, StoredBytes: p.Snapshot.StoredBytes, Capture: a.Capture.Clone(), CaptureToken: g.Token}
	return b, a, nil
}

type admittedSnapshotClient interface {
	CaptureAdmittedRuntime(context.Context, runtimeadmission.SnapshotGrant) (SnapshotBytes, runtimeadmission.SnapshotAcknowledgment, error)
}

func (r *VMMRouter) CaptureAdmittedRuntime(ctx context.Context, nodeID string, g runtimeadmission.SnapshotGrant) (SnapshotBytes, runtimeadmission.SnapshotAcknowledgment, error) {
	if g.Parent.Binding.NodeID != nodeID {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, runtimeadmission.ErrStale
	}
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	native, ok := cli.(admittedSnapshotClient)
	if !ok {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, runtimeadmission.ErrUnavailable
	}
	return native.CaptureAdmittedRuntime(ctx, g)
}
