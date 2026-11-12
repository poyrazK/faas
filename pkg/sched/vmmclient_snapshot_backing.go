// adr: 20261106214916

package sched

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// SnapshotBackingVerdict is vmmd's answer to whether a parked capture would
// restore on its node (ADR-20261106214916).
type SnapshotBackingVerdict string

const (
	// SnapshotBackingCurrent: a wake on the node would restore the capture.
	SnapshotBackingCurrent SnapshotBackingVerdict = "current"
	// SnapshotBackingStale: a wake on the node would refuse the capture and
	// cold-boot.
	SnapshotBackingStale SnapshotBackingVerdict = "stale"
	// SnapshotBackingUnknown: the node could not tell; a wake decides.
	SnapshotBackingUnknown SnapshotBackingVerdict = "unknown"
)

// ErrSnapshotBackingUnsupported is a vmmd that predates SnapshotBackingCurrent.
var ErrSnapshotBackingUnsupported = errors.New("sched: vmmd does not answer SnapshotBackingCurrent")

// SnapshotBackingCurrent asks this vmmd whether snap would restore with
// baseKey as drive0. A refused request (a key vmmd will not check) is an
// unknown verdict, not an error; an error means vmmd did not answer.
func (c *VMMClient) SnapshotBackingCurrent(ctx context.Context, snap SnapshotRef, baseKey string) (SnapshotBackingVerdict, string, error) {
	resp, err := c.cli.SnapshotBackingCurrent(ctx, &vmmdpb.SnapshotBackingCurrentRequest{
		Snapshot: &vmmdpb.SnapshotRef{
			DeploymentId: snap.DeploymentID, VmstatePath: snap.VMStatePath, FcVersion: snap.FCVersion,
			StorageKey: snap.StorageKey, VmstateStorageKey: snap.VMStateStorageKey, MemBytes: snap.MemBytes,
		},
		BaseKey: baseKey,
	})
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented:
		return SnapshotBackingUnknown, "", ErrSnapshotBackingUnsupported
	case codes.InvalidArgument:
		return SnapshotBackingUnknown, status.Convert(err).Message(), nil
	default:
		return SnapshotBackingUnknown, "", liftErr(err)
	}
	switch resp.GetVerdict() {
	case vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_CURRENT:
		return SnapshotBackingCurrent, resp.GetDetail(), nil
	case vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_STALE:
		return SnapshotBackingStale, resp.GetDetail(), nil
	default:
		return SnapshotBackingUnknown, resp.GetDetail(), nil
	}
}

type snapshotBackingClient interface {
	SnapshotBackingCurrent(ctx context.Context, snap SnapshotRef, baseKey string) (SnapshotBackingVerdict, string, error)
}

// SnapshotBackingCurrent asks nodeID's vmmd.
func (r *VMMRouter) SnapshotBackingCurrent(ctx context.Context, nodeID string, snap SnapshotRef, baseKey string) (SnapshotBackingVerdict, string, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return SnapshotBackingUnknown, "", err
	}
	checker, ok := cli.(snapshotBackingClient)
	if !ok {
		return SnapshotBackingUnknown, "", ErrSnapshotBackingUnsupported
	}
	return checker.SnapshotBackingCurrent(ctx, snap, baseKey)
}
