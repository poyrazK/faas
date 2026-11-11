package fcvm

import (
	"context"
	"errors"
	"fmt"
)

// SnapshotBackingVerdict is whether a capture would restore on this node
// (ADR-20261106214916).
type SnapshotBackingVerdict int

const (
	// SnapshotBackingUnknown: this node could not tell, because its own images
	// or the storage holding the capture's identity could not be read. A wake
	// decides; the platform does not refresh on a guess.
	SnapshotBackingUnknown SnapshotBackingVerdict = iota
	// SnapshotBackingCurrent: a wake here would restore the capture.
	SnapshotBackingCurrent
	// SnapshotBackingStale: a wake here would refuse the capture and cold-boot:
	// it was made by another Firecracker version, onto other kernel or base
	// images or guest timers, or it has no valid recorded identity.
	SnapshotBackingStale
)

func (v SnapshotBackingVerdict) String() string {
	switch v {
	case SnapshotBackingCurrent:
		return "current"
	case SnapshotBackingStale:
		return "stale"
	default:
		return "unknown"
	}
}

// CheckSnapshotBackingCurrent answers, without starting a VM, whether snap
// would restore on this node with baseKey as drive0: the Firecracker version
// pin (ADR-005) and ADR-510's backing check, the same checks a wake runs
// before it loads a snapshot. The error carries the reason for operators.
func (m *Manager) CheckSnapshotBackingCurrent(ctx context.Context, snap *Snapshot, baseKey string) (SnapshotBackingVerdict, error) {
	if snap == nil || snap.StorageKey == "" || baseKey == "" {
		return SnapshotBackingUnknown, errors.New("fcvm: snapshot backing check needs a snapshot storage key and a base key")
	}
	if snap.FCVersion != "" && snap.FCVersion != m.fcVersion {
		return SnapshotBackingStale, fmt.Errorf("%w: captured by Firecracker %s, this node runs %s", ErrSnapshotBackingChanged, snap.FCVersion, m.fcVersion)
	}
	_, err := m.checkSnapshotBacking(ctx, snap, baseKey)
	switch {
	case err == nil:
		return SnapshotBackingCurrent, nil
	case errors.Is(err, errBackingCheckFailed):
		return SnapshotBackingUnknown, err
	case errors.Is(err, ErrSnapshotBackingChanged), errors.Is(err, ErrSnapshotBackingUnverified):
		return SnapshotBackingStale, err
	default:
		return SnapshotBackingUnknown, err
	}
}
