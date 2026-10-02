package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardNativeSnapshotVMM interface {
	CaptureAdmittedRuntime(context.Context, string, runtimeadmission.SnapshotGrant) (SnapshotBytes, runtimeadmission.SnapshotAcknowledgment, error)
}

func (e *Engine) measuredSnapshotSource(ctx context.Context, ins state.Instance) (bool, error) {
	capture, err := e.capturedStandardRuntime(ctx, ins.AppID, ins.ID)
	if err != nil {
		return false, applicationStandardRuntimeProblem(err)
	}
	if !capture.Managed {
		return false, nil
	}
	reader, ok := e.store.(state.InstanceApplicationStandardRuntimeReceiptStore)
	if !ok {
		return false, runtimeadmission.ErrUnavailable
	}
	r, err := reader.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID)
	if err != nil {
		return false, applicationStandardRuntimeProblem(err)
	}
	if r.Binding.InstanceID != ins.ID || r.Binding.NodeID != ins.NodeID || r.Binding.AppID != canonicalStandardNativeUUID(ins.AppID) || r.Binding.DeploymentID != canonicalStandardNativeUUID(ins.DeploymentID) {
		return false, runtimeadmission.ErrStale
	}
	return r.Binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion, nil
}

func (e *Engine) captureSnapshotWithStandards(ctx context.Context, ins state.Instance, vmstate, memKey, stateKey string, before bool, mode string) (SnapshotBytes, error) {
	measured, err := e.measuredSnapshotSource(ctx, ins)
	if err != nil {
		return SnapshotBytes{}, err
	}
	if !measured {
		if mode == "warm" {
			return e.vmm.WarmSnapshot(ctx, ins.NodeID, ins.ID, memKey, stateKey)
		}
		return e.vmm.PauseAndSnapshot(ctx, ins.NodeID, ins.ID, vmstate, memKey, stateKey, before)
	}
	store, ok := e.store.(state.ApplicationStandardSnapshotCaptureStore)
	if !ok {
		return SnapshotBytes{}, runtimeadmission.ErrUnavailable
	}
	native, ok := e.vmm.(standardNativeSnapshotVMM)
	if !ok {
		return SnapshotBytes{}, runtimeadmission.ErrUnavailable
	}
	g, err := e.issueStandardSnapshotCapture(ctx, store, ins, memKey, stateKey, before, mode)
	if err != nil {
		return SnapshotBytes{}, applicationStandardRuntimeProblem(err)
	}
	b, ack, err := native.CaptureAdmittedRuntime(ctx, ins.NodeID, g)
	if err != nil {
		return SnapshotBytes{}, err
	}
	if !b.Capture.Equal(ack.Capture) || b.MemBytes != ack.Capture.Memory.Bytes || b.VMStateBytes != ack.Capture.VMState.Bytes || b.StoredBytes < 0 || ack.Check(g, time.Now()) != nil {
		return SnapshotBytes{}, runtimeadmission.ErrInvalid
	}
	if err := store.PublishApplicationStandardSnapshotCapture(ctx, ack); err != nil {
		return SnapshotBytes{}, applicationStandardRuntimeProblem(err)
	}
	b.Capture, b.CaptureToken = ack.Capture.Clone(), g.Token
	return b, nil
}

func (e *Engine) issueStandardSnapshotCapture(ctx context.Context, store state.ApplicationStandardSnapshotCaptureStore, ins state.Instance, memKey, stateKey string, before bool, mode string) (runtimeadmission.SnapshotGrant, error) {
	token, ok := state.SnapshotCaptureToken(memKey)
	if !ok {
		return runtimeadmission.SnapshotGrant{}, runtimeadmission.ErrInvalid
	}
	expectedState := string(state.StateSnapshotting)
	if mode == "warm" {
		expectedState = string(state.StateRunning)
	}
	req := state.ApplicationStandardSnapshotCaptureRequest{Token: token, InstanceID: ins.ID, MemoryKey: memKey, VMStateKey: stateKey, PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: memKey}), FCVersion: e.fcVer, Mode: mode, BeforeCheckpoint: before, SourceStartedAtUnixNano: ins.StartedAt.UnixNano()}
	return store.IssueApplicationStandardSnapshotCapture(ctx, expectedState, req)
}
