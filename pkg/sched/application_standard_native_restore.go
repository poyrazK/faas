package sched

// adr: 595 Catalog selection becomes authority only at durable boot issuance.

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) prepareStandardSnapshotRestore(ctx context.Context, identity runtimeadmission.Identity, req *vmmdpb.CreateAdmittedRuntimeRequest, snapshot *SnapshotRef) (*vmmdpb.CreateAdmittedRuntimeRequest, error) {
	if snapshot == nil || snapshot.ApplicationStandardCaptureToken == "" || identity.SnapshotRestoreVersion != runtimeadmission.SnapshotRestoreVersion {
		return req, nil
	}
	store, ok := e.store.(state.ApplicationStandardSnapshotCaptureStore)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	binding, err := runtimeadmission.BindingFromProto(req.Binding)
	if err != nil {
		return nil, err
	}
	record, err := store.GetApplicationStandardSnapshotCapture(ctx, binding.AccountID, binding.AppID, binding.DeploymentID, snapshot.ApplicationStandardCaptureToken)
	// A collected or missing cache is not a dependency of verified cold boot.
	if errors.Is(err, state.ErrNotFound) {
		return req, nil
	}
	if err != nil {
		return nil, applicationStandardRuntimeProblem(err)
	}
	evidence, err := state.StandardSnapshotRestoreEvidence(record)
	if err != nil {
		return req, nil
	}
	binding.SnapshotCaptureToken = evidence.CaptureToken
	binding.SnapshotEvidenceHash, err = evidence.Hash()
	if err != nil {
		return nil, err
	}
	if req.GetRestore().Snapshot.VmstateStorageKey == "" {
		req.GetRestore().Snapshot.VmstateStorageKey = evidence.Capture.VMState.StorageKey
		req.GetRestore().Snapshot.VmstatePath = ""
	}
	req.SnapshotRestore = evidence.ToProto()
	req.Binding = binding.ToProto()
	if err := runtimeadmission.CheckSnapshotRestorePayload(req, binding, time.Now()); err != nil {
		return nil, err
	}
	binding.PayloadHash, err = runtimeadmission.HashBootPayload(req)
	if err != nil {
		return nil, err
	}
	req.Binding = binding.ToProto()
	return req, nil
}
