package fcvm

import (
	"context"
	"runtime"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type consumedSourceVMM interface {
	verifiedSourceVMM
	SupportsRuntimeArtifactConsumption() bool
	ObservedRuntimeDrives(context.Context, Lease) (RuntimeDriveHandoffObservation, error)
}

func (v *JailerVMM) SupportsRuntimeArtifactConsumption() bool {
	return v != nil && runtime.GOOS == "linux"
}

func (m *Manager) runtimeAdmissionProtocol() uint32 {
	if v, ok := m.vmm.(consumedSourceVMM); ok && v.SupportsRuntimeArtifactConsumption() {
		return runtimeadmission.ArtifactProtocolVersion
	}
	return runtimeadmission.ProtocolVersion
}

func checkAdmittedArtifactHash(binding runtimeadmission.Binding, req WakeRequest) error {
	if binding.ProtocolVersion == runtimeadmission.ProtocolVersion {
		return nil
	}
	if req.KeepPaused && req.SnapshotRestore == nil {
		return runtimeadmission.ErrUnavailable
	}
	hash, err := runtimeadmission.HashArtifactSources(req.ArtifactSources)
	if err != nil || hash != binding.ArtifactSourcesHash {
		return runtimeadmission.ErrInvalid
	}
	return nil
}

func (m *Manager) admittedArtifactConsumption(ctx context.Context, binding runtimeadmission.Binding, inst *Instance) (runtimeadmission.ArtifactConsumption, error) {
	if binding.ProtocolVersion == runtimeadmission.ProtocolVersion {
		return runtimeadmission.ArtifactConsumption{}, nil
	}
	v, ok := m.vmm.(consumedSourceVMM)
	if !ok || !v.SupportsRuntimeArtifactConsumption() {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.ErrUnavailable
	}
	observation, err := v.ObservedRuntimeDrives(ctx, inst.Lease)
	if err != nil {
		return runtimeadmission.ArtifactConsumption{}, err
	}
	if observation.InstanceID != binding.InstanceID || observation.LeaseUID != inst.Lease.UID || observation.ProcessPID <= 0 || uint64(observation.ProcessPID) > uint64(^uint32(0)) {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.ErrStale
	}
	consumption := runtimeConsumptionFromObservation(observation)
	if err := consumption.Check(binding.ArtifactSourcesHash); err != nil {
		return runtimeadmission.ArtifactConsumption{}, err
	}
	return consumption, ctx.Err()
}

func runtimeConsumptionFromObservation(observation RuntimeDriveHandoffObservation) runtimeadmission.ArtifactConsumption {
	consumption := runtimeadmission.ArtifactConsumption{ConfigHash: observation.ConfigHash, ProcessPID: uint32(observation.ProcessPID), ProcessStart: observation.ProcessStart}
	for _, d := range observation.Drives {
		consumption.Drives = append(consumption.Drives, runtimeadmission.ConsumedDrive{Source: d.Source, DriveID: d.DriveID, ReadOnly: d.ReadOnly, RootDevice: d.RootDevice, ProducerDigest: d.Producer.Digest, ProducerBytes: d.Producer.Bytes, InjectedDigest: d.Injected.Digest, InjectedBytes: d.Injected.Bytes})
	}
	return consumption
}
