//go:build linux

package fcvm

import (
	"context"
	"errors"
	"net"
	"time"
)

type linuxNativeQualificationRestoreResume struct{}

func newNativeQualificationRestoreResumeBackend() nativeQualificationRestoreResumeBackend {
	return linuxNativeQualificationRestoreResume{}
}

func (linuxNativeQualificationRestoreResume) Resume(ctx context.Context, v *JailerVMM, owner nativeLaunchRecord, receipt qualificationRestoreReceiptControl) (result error) {
	if !liveNativeSnapshotOwner(owner) {
		return errors.New("native restore hook: original live target is required")
	}
	handle, err := openNativeProcess(owner.PID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, handle.Close()) }()
	pinned := handle.(*nativePIDFD)
	if err := checkNativeSnapshotProcess(pinned, owner); err != nil {
		return err
	}
	// The receipt control crosses only this peer-checked connection to the
	// pinned original process (ADR 20261023093000).
	return v.triggerResumeHookOnceWithPeer(ctx, owner.Lease, time.Now().UnixNano(), receipt, func(connection net.Conn) error {
		return errors.Join(checkNativeSnapshotPeer(connection, owner), checkNativeSnapshotProcess(pinned, owner), ctx.Err())
	})
}
