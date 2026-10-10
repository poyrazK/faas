//go:build !linux

package fcvm

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

func (*JailerVMM) captureEnvironmentQualificationSnapshot(context.Context, Lease, BackingIdentity) (SnapshotInfo, error) {
	return SnapshotInfo{}, fmt.Errorf("native snapshot capture requires Linux: %w", state.ErrConflict)
}
