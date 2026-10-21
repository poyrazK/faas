//go:build !linux

package fcvm

import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func observeSnapshotMemoryMaps(context.Context, string, int, int, pinnedRuntimeDrive) (string, []RuntimeSnapshotMemoryRange, error) {
	return "", nil, runtimeadmission.ErrUnavailable
}
