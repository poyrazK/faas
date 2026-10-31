//go:build !linux

// adr: 20261022184951 — the native producer exists only on Linux.
package fcvm

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

func (v *JailerVMM) captureNativeRuntimeSnapshot(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
	return SnapshotInfo{}, fmt.Errorf("native recovery: snapshot publication producer is unavailable: %w", state.ErrConflict)
}
