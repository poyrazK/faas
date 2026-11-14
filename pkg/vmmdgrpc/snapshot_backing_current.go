package vmmdgrpc

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
)

// snapshotBackingChecker is the fcvm.Manager surface SnapshotBackingCurrent
// needs.
type snapshotBackingChecker interface {
	CheckSnapshotBackingCurrent(ctx context.Context, snap *fcvm.Snapshot, baseKey string) (fcvm.SnapshotBackingVerdict, error)
}

// SnapshotBackingCurrent tells schedd whether a parked capture would restore
// on this node, without starting a VM (ADR-20261106214916). It reads the
// capture's backing identity and identifies this node's kernel and base, the
// checks a wake runs before it loads a snapshot; it never writes a capture.
// Only a capture key and a base key are accepted, so the RPC cannot be used
// to read other storage through vmmd.
func (s *Server) SnapshotBackingCurrent(ctx context.Context, req *vmmdpb.SnapshotBackingCurrentRequest) (*vmmdpb.SnapshotBackingCurrentResponse, error) {
	const op = "SnapshotBackingCurrent"
	start := time.Now()
	ref := req.GetSnapshot()
	if !state.IsSnapshotCaptureKey(ref.GetStorageKey()) || !validBackingBaseKey(req.GetBaseKey()) {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid snapshot backing check", "snapshot.storage_key must be a capture key and base_key a base image key")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	checker, ok := s.vmm.(snapshotBackingChecker)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Snapshot backing check unavailable", "vmmd does not expose the snapshot backing check")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	snap := &fcvm.Snapshot{
		DeploymentID: ref.GetDeploymentId(), VMStatePath: ref.GetVmstatePath(),
		FCVersion: ref.GetFcVersion(), StorageKey: ref.GetStorageKey(),
		VMStateStorageKey: ref.GetVmstateStorageKey(), MemBytes: ref.GetMemBytes(),
	}
	verdict, err := checker.CheckSnapshotBackingCurrent(ctx, snap, req.GetBaseKey())
	// A stale or unknown verdict is an answer, not a failed call.
	s.ops.Observe(op, time.Since(start), nil)
	resp := &vmmdpb.SnapshotBackingCurrentResponse{Verdict: snapshotBackingVerdictToProto(verdict)}
	if err != nil {
		resp.Detail = err.Error()
	}
	return resp, nil
}

// validBackingBaseKey accepts the drive0 keys schedd boots: runtime bases,
// generations and runtime releases all live under base/.
func validBackingBaseKey(key string) bool {
	return strings.HasPrefix(key, "base/") && len(key) > len("base/") && !strings.Contains(key, "..")
}

func snapshotBackingVerdictToProto(v fcvm.SnapshotBackingVerdict) vmmdpb.SnapshotBackingVerdict {
	switch v {
	case fcvm.SnapshotBackingCurrent:
		return vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_CURRENT
	case fcvm.SnapshotBackingStale:
		return vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_STALE
	default:
		return vmmdpb.SnapshotBackingVerdict_SNAPSHOT_BACKING_UNKNOWN
	}
}
