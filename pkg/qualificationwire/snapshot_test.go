// adr: 567 — capture evidence must retain original deployment and coupled objects.
package qualificationwire

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/proto"
)

func wireSnapshot(frame state.EnvironmentQualificationExecution) state.EnvironmentQualificationSnapshot {
	id := uuid.NewString()
	mem := state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, id)
	s := state.Snapshot{StorageKey: mem}
	return state.EnvironmentQualificationSnapshot{CaptureID: id, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		StorageKey: mem, VMStateStorageKey: state.SnapshotVMStateKey(s), DriveStorageKey: state.SnapshotDriveKey(s), BackingStorageKey: state.SnapshotBackingKey(s),
		MemBytes: 1 << 33, VMStateBytes: 1 << 32, StoredBytes: 1 << 34}
}

func TestSnapshotWirePreservesOriginalCaptureAndRejectsSubstitution(t *testing.T) {
	frame := wireFrame()
	proof := wireSnapshot(frame)
	p, err := SnapshotToProto(frame, proof)
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var received vmmdpb.EnvironmentQualificationSnapshot
	if err := proto.Unmarshal(data, &received); err != nil {
		t.Fatal(err)
	}
	if actual, err := SnapshotFromProto(frame, &received); err != nil || actual != proof {
		t.Fatal("capture contract changed or truncated", err)
	}
	for _, failure := range []string{"nil", "version", "unknown", "capture", "physical", "boot", "same_generation", "mem", "vmstate", "drive", "backing", "mem_bytes", "vmstate_bytes", "stored_bytes", "deployment"} {
		t.Run(failure, func(t *testing.T) {
			changed := proto.Clone(p).(*vmmdpb.EnvironmentQualificationSnapshot)
			original := frame
			switch failure {
			case "nil":
				changed = nil
			case "version":
				changed.ContractVersion++
			case "unknown":
				changed.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "capture":
				changed.CaptureId = uuid.NewString()
			case "physical":
				changed.NativeGeneration = ""
			case "boot":
				changed.KernelBootId = ""
			case "same_generation":
				changed.NativeGeneration = changed.CaptureId
			case "mem":
				changed.StorageKey = state.WarmSnapMemKey(frame.DeploymentID)
			case "vmstate":
				changed.VmstateStorageKey += "other"
			case "drive":
				changed.DriveStorageKey = ""
			case "backing":
				changed.BackingStorageKey = ""
			case "mem_bytes":
				changed.MemBytes = -1
			case "vmstate_bytes":
				changed.VmstateBytes = 0
			case "stored_bytes":
				changed.StoredBytes = 0
			case "deployment":
				original.DeploymentID = uuid.NewString()
			}
			if actual, err := SnapshotFromProto(original, changed); !errors.Is(err, state.ErrConflict) || actual != (state.EnvironmentQualificationSnapshot{}) {
				t.Fatal("substituted or incomplete capture supplied evidence", err)
			}
		})
	}
}
