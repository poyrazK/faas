// adr: 567 — the private wire must preserve the complete original capability.
package qualificationwire

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/proto"
)

func wireFrame() state.EnvironmentQualificationExecution {
	return state.EnvironmentQualificationExecution{InstanceID: uuid.NewString(), RequestID: uuid.NewString(), GraphID: uuid.NewString(),
		AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), SourceID: uuid.NewString(),
		EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), CleanupToken: uuid.NewString(), Resource: "workload/api", Scope: "production",
		PlanHash: strings.Repeat("a", 64), Generation: 1 << 42, IntentVersion: 1 << 41, Attempt: 1 << 36, RAMMB: 512,
		Artifact: state.EnvironmentWorkloadArtifact{RootfsPath: "/private/original.ext4", RootfsKey: "apps/original.ext4", RootfsBytes: 1 << 33,
			ImageDigest: "sha256:" + strings.Repeat("b", 64), BuildID: uuid.NewString(), Kind: state.DeploymentKindDockerfile, CommitSHA: strings.Repeat("c", 40)}}
}

func TestExecutionWirePreservesEveryField(t *testing.T) {
	for _, compact := range []bool{false, true} {
		frame := wireFrame()
		if compact {
			frame.NodeID = strings.ReplaceAll(frame.NodeID, "-", "")
		}
		encoded, err := ExecutionToProto(frame)
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var received vmmdpb.EnvironmentQualificationExecution
		if err := proto.Unmarshal(data, &received); err != nil {
			t.Fatal(err)
		}
		actual, err := ExecutionFromProto(&received)
		if err != nil || actual != frame {
			t.Fatal("wire lost or normalized original execution authority", err)
		}
	}
}

func TestExecutionWireRejectsIncompleteOrUnknownAuthority(t *testing.T) {
	for _, failure := range []string{"nil", "version", "artifact", "frame_unknown", "artifact_unknown", "cleanup", "negative_memory", "hash", "generation", "kind"} {
		t.Run(failure, func(t *testing.T) {
			p, err := ExecutionToProto(wireFrame())
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "nil":
				p = nil
			case "version":
				p.ContractVersion = 2
			case "artifact":
				p.Artifact = nil
			case "frame_unknown":
				p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "artifact_unknown":
				p.Artifact.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "cleanup":
				p.CleanupToken = ""
			case "negative_memory":
				p.RamMb = -1
			case "hash":
				p.PlanHash = strings.Repeat("A", 64)
			case "generation":
				p.Generation = 0
			case "kind":
				p.Artifact.Kind = "unknown"
			}
			if _, err := ExecutionFromProto(p); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal("invalid frame accepted", err)
			}
		})
	}
	frame := wireFrame()
	frame.RAMMB = math.MaxInt32 + 1
	if _, err := ExecutionToProto(frame); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("memory reservation truncated on wire", err)
	}
}

func TestNativeRetirementWireRefusesGenericAbsence(t *testing.T) {
	proof := state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(),
		NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
	p, err := NativeRetirementToProto(proof)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := NativeRetirementFromProto(p)
	if err != nil || actual != proof {
		t.Fatal("retirement proof changed", err)
	}
	for _, failure := range []string{"nil", "unknown", "never_dispatched", "exit", "resources", "receipt", "native_generation", "boot"} {
		t.Run(failure, func(t *testing.T) {
			changed := proto.Clone(p).(*vmmdpb.EnvironmentQualificationRetirement)
			switch failure {
			case "nil":
				changed = nil
			case "unknown":
				changed.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "never_dispatched":
				changed.Kind = state.QualificationNeverDispatched
			case "exit":
				changed.ProcessesExited = false
			case "resources":
				changed.ResourcesRemoved = false
			case "receipt":
				changed.ReceiptId = ""
			case "native_generation":
				changed.NativeGeneration = ""
			case "boot":
				changed.KernelBootId = ""
			}
			if _, err := NativeRetirementFromProto(changed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("absence acknowledged retirement", err)
			}
		})
	}
}
