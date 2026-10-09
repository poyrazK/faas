package qualificationwire

import (
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateSnapshot(frame state.EnvironmentQualificationExecution, proof state.EnvironmentQualificationSnapshot) error {
	if err := validateExecution(frame); err != nil {
		return err
	}
	return state.ValidateEnvironmentQualificationSnapshot(frame, proof)
}

func SnapshotToProto(frame state.EnvironmentQualificationExecution, proof state.EnvironmentQualificationSnapshot) (*vmmdpb.EnvironmentQualificationSnapshot, error) {
	if err := validateSnapshot(frame, proof); err != nil {
		return nil, err
	}
	return &vmmdpb.EnvironmentQualificationSnapshot{ContractVersion: contractVersion, CaptureId: proof.CaptureID, NativeGeneration: proof.NativeGeneration,
		KernelBootId: proof.KernelBootID, FcVersion: proof.FCVersion, StorageKey: proof.StorageKey, VmstateStorageKey: proof.VMStateStorageKey,
		DriveStorageKey: proof.DriveStorageKey, BackingStorageKey: proof.BackingStorageKey,
		MemBytes: proof.MemBytes, VmstateBytes: proof.VMStateBytes, StoredBytes: proof.StoredBytes}, nil
}

func SnapshotFromProto(frame state.EnvironmentQualificationExecution, p *vmmdpb.EnvironmentQualificationSnapshot) (state.EnvironmentQualificationSnapshot, error) {
	if p == nil || p.GetContractVersion() != contractVersion || len(p.ProtoReflect().GetUnknown()) != 0 {
		return state.EnvironmentQualificationSnapshot{}, fmt.Errorf("qualification capture wire profile is unsupported: %w", state.ErrConflict)
	}
	proof := state.EnvironmentQualificationSnapshot{CaptureID: p.GetCaptureId(), NativeGeneration: p.GetNativeGeneration(), KernelBootID: p.GetKernelBootId(),
		FCVersion: p.GetFcVersion(), StorageKey: p.GetStorageKey(), VMStateStorageKey: p.GetVmstateStorageKey(), DriveStorageKey: p.GetDriveStorageKey(),
		BackingStorageKey: p.GetBackingStorageKey(), MemBytes: p.GetMemBytes(), VMStateBytes: p.GetVmstateBytes(), StoredBytes: p.GetStoredBytes()}
	if err := validateSnapshot(frame, proof); err != nil {
		return state.EnvironmentQualificationSnapshot{}, err
	}
	return proof, nil
}
