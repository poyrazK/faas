//go:build !linux && !darwin

package fcvm

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
)

func (v *JailerVMM) checkNativeSnapshotCaptureSupport() error { return state.ErrConflict }
func (v *JailerVMM) preflightNativeSnapshotCapture(context.Context, Lease, SnapshotSpec) error {
	return state.ErrConflict
}
func (v *JailerVMM) nativeSnapshotKeepAlive(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
	return SnapshotInfo{}, state.ErrConflict
}
