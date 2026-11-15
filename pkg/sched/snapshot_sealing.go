package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// errSnapshotSealingUnavailable refuses a capture when sealing is on but the
// node's vmmd client cannot seal at the source.
var errSnapshotSealingUnavailable = errors.New("sched: sealed snapshots need a vmmd that seals them")

// snapshotSealer is the VMM capability behind sealed park and warm snapshots
// (ADR-20261110210000 slice 3d-3).
type snapshotSealer interface {
	PauseAndSnapshotSealed(ctx context.Context, nodeID, instance, storageKey, vmstateStorageKey string, beforeCheckpoint bool, captureID string) (SnapshotBytes, error)
	WarmSnapshotSealed(ctx context.Context, nodeID, instance, storageKey, vmstateStorageKey, captureID string) (SnapshotBytes, []byte, error)
}

// WithSealedSnapshots makes park and warm captures sealed at the source
// (ADR-20261110210000 slice 3d-3): vmmd publishes only age-encrypted twins
// and the capture key reaches the snapshots row sealed to the capture
// recipient, so a node's object-store credentials neither read nor silently
// rewrite another app's snapshot. Off until metal tests and the restore p95
// measurement pass.
func (e *Engine) WithSealedSnapshots(sealed bool) *Engine {
	e.sealSnapshots = sealed
	return e
}

// snapshotSealToken reports whether this capture is sealed and its capture
// id. Only storage-backed captures with a capture-token key are sealed; a
// legacy host-path vmstate stays on the node and never reaches the object
// store.
func (e *Engine) snapshotSealToken(memKey, stateKey string) (string, bool) {
	if !e.sealSnapshots || stateKey == "" {
		return "", false
	}
	return state.SnapshotCaptureToken(memKey)
}

func (e *Engine) captureSealedSnapshot(ctx context.Context, ins state.Instance, memKey, stateKey string, before bool, mode, token string) (SnapshotBytes, error) {
	sealer, ok := e.vmm.(snapshotSealer)
	if !ok {
		return SnapshotBytes{}, errSnapshotSealingUnavailable
	}
	if mode == "warm" {
		b, key, err := sealer.WarmSnapshotSealed(ctx, ins.NodeID, ins.ID, memKey, stateKey, token)
		if err != nil {
			return SnapshotBytes{}, err
		}
		b.SealedKey, b.SealCaptureID = key, token
		return b, nil
	}
	return sealer.PauseAndSnapshotSealed(ctx, ins.NodeID, ins.ID, memKey, stateKey, before, token)
}
