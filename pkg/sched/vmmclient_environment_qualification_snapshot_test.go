// adr: 568 — portable RPC checks do not substitute for native capture acceptance.
package sched_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func qualificationRPCSnapshot(frame state.EnvironmentQualificationExecution) state.EnvironmentQualificationSnapshot {
	id := uuid.NewString()
	mem := state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, id)
	s := state.Snapshot{StorageKey: mem}
	return state.EnvironmentQualificationSnapshot{CaptureID: id, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		FCVersion:  "1.7.0",
		StorageKey: mem, VMStateStorageKey: state.SnapshotVMStateKey(s), DriveStorageKey: state.SnapshotDriveKey(s), BackingStorageKey: state.SnapshotBackingKey(s),
		MemBytes: 1 << 33, VMStateBytes: 64, StoredBytes: 1 << 32}
}

type qualificationSnapshotRPCVMM struct {
	*qualificationRPCVMM
	frame state.EnvironmentQualificationExecution
	proof state.EnvironmentQualificationSnapshot
	calls atomic.Int32
	err   error
}

func (v *qualificationSnapshotRPCVMM) CaptureEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (state.EnvironmentQualificationSnapshot, error) {
	v.calls.Add(1)
	if frame != v.frame {
		return state.EnvironmentQualificationSnapshot{}, state.ErrConflict
	}
	return v.proof, v.err
}

func TestVMMClientEnvironmentQualificationSnapshotPreservesOriginalFrame(t *testing.T) {
	frame := qualificationRPCFrame()
	v := &qualificationSnapshotRPCVMM{qualificationRPCVMM: &qualificationRPCVMM{fakeVMM: &fakeVMM{}}, frame: frame, proof: qualificationRPCSnapshot(frame)}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
	proof, err := client.CaptureEnvironmentQualification(t.Context(), frame)
	if err != nil || proof.Execution != frame || proof.Snapshot != v.proof || v.calls.Load() != 1 {
		t.Fatal("RPC lost original attempt or complete capture evidence", err)
	}
}

func TestEnvironmentQualificationSnapshotRPCRejectsEnvelopeBeforeEffects(t *testing.T) {
	for _, failure := range []string{"nil", "version", "unknown_frame", "unknown_request", "node", "metadata", "duplicate_metadata"} {
		t.Run(failure, func(t *testing.T) {
			frame := qualificationRPCFrame()
			v := &qualificationSnapshotRPCVMM{qualificationRPCVMM: &qualificationRPCVMM{fakeVMM: &fakeVMM{}}, frame: frame, proof: qualificationRPCSnapshot(frame)}
			client := vmmdpb.NewVmmdClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
			encoded, err := qualificationwire.ExecutionToProto(frame)
			if err != nil {
				t.Fatal(err)
			}
			req := &vmmdpb.CaptureEnvironmentQualificationRequest{Execution: encoded}
			ctx := qualificationRPCMetadata(t.Context(), frame)
			want := codes.InvalidArgument
			switch failure {
			case "nil":
				req = nil
			case "version":
				req.Execution.ContractVersion = 0
			case "unknown_frame":
				req.Execution.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "unknown_request":
				req.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "node":
				req.Execution.NodeId = uuid.NewString()
				want = codes.FailedPrecondition
			case "metadata":
				changed := frame
				changed.WakeID = uuid.NewString()
				ctx = qualificationRPCMetadata(t.Context(), changed)
			case "duplicate_metadata":
				ctx = metadata.AppendToOutgoingContext(ctx, "x-faas-instance-id", frame.InstanceID)
			}
			if _, err := client.CaptureEnvironmentQualification(ctx, req); status.Code(err) != want || v.calls.Load() != 0 {
				t.Fatal("invalid capture envelope reached owner", err)
			}
		})
	}
}

type qualificationSnapshotReplyServer struct {
	vmmdpb.UnimplementedVmmdServer
	change func(*vmmdpb.CaptureEnvironmentQualificationResponse)
}

func (s *qualificationSnapshotReplyServer) CaptureEnvironmentQualification(_ context.Context, req *vmmdpb.CaptureEnvironmentQualificationRequest) (*vmmdpb.CaptureEnvironmentQualificationResponse, error) {
	frame, err := qualificationwire.ExecutionFromProto(req.GetExecution())
	if err != nil {
		return nil, err
	}
	proof, err := qualificationwire.SnapshotToProto(frame, qualificationRPCSnapshot(frame))
	if err != nil {
		return nil, err
	}
	r := &vmmdpb.CaptureEnvironmentQualificationResponse{Execution: proto.Clone(req.Execution).(*vmmdpb.EnvironmentQualificationExecution), Snapshot: proof}
	s.change(r)
	return r, nil
}

func TestVMMClientEnvironmentQualificationSnapshotRejectsSubstitutedReplies(t *testing.T) {
	for _, failure := range []string{"frame", "artifact", "scope", "attempt", "cleanup", "nil_frame", "nil_snapshot", "version", "unknown_reply", "unknown_snapshot", "namespace", "private_drive", "backing", "bytes"} {
		t.Run(failure, func(t *testing.T) {
			s := &qualificationSnapshotReplyServer{change: func(r *vmmdpb.CaptureEnvironmentQualificationResponse) {
				switch failure {
				case "frame":
					r.Execution.NodeId = uuid.NewString()
				case "artifact":
					r.Execution.Artifact.RootfsBytes++
				case "scope":
					r.Execution.Scope = "staging"
				case "attempt":
					r.Execution.Attempt++
				case "cleanup":
					r.Execution.CleanupToken = uuid.NewString()
				case "nil_frame":
					r.Execution = nil
				case "nil_snapshot":
					r.Snapshot = nil
				case "version":
					r.Snapshot.ContractVersion++
				case "unknown_reply":
					r.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
				case "unknown_snapshot":
					r.Snapshot.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
				case "namespace":
					r.Snapshot.StorageKey = "snap/shared/warm/mem"
				case "private_drive":
					r.Snapshot.DriveStorageKey = ""
				case "backing":
					r.Snapshot.BackingStorageKey = ""
				case "bytes":
					r.Snapshot.VmstateBytes = 0
				}
			}}
			client := sched.NewVMMClient(qualificationRPCConnection(t, s))
			proof, err := client.CaptureEnvironmentQualification(t.Context(), qualificationRPCFrame())
			if !errors.Is(err, state.ErrConflict) || proof != (sched.EnvironmentQualificationSnapshotEvidence{}) {
				t.Fatal("substituted reply supplied qualification evidence", err)
			}
		})
	}
}

func TestEnvironmentQualificationSnapshotRPCFailsClosedAndRedactsPrivateErrors(t *testing.T) {
	frame := qualificationRPCFrame()
	for _, failure := range []string{"older", "disabled_manager", "private_error", "bad_proof"} {
		t.Run(failure, func(t *testing.T) {
			v := &qualificationSnapshotRPCVMM{qualificationRPCVMM: &qualificationRPCVMM{fakeVMM: &fakeVMM{}}, frame: frame, proof: qualificationRPCSnapshot(frame)}
			server := qualificationRPCServer(v, frame.NodeID)
			want := api.CodeEnvironmentQualificationUnconfirmed
			switch failure {
			case "older":
				server = qualificationRPCServer(v.qualificationRPCVMM, frame.NodeID)
				want = api.CodeNotImplemented
			case "disabled_manager":
				server = qualificationRPCServer(qualificationManagerRPC{fcvm.NewManager(nil, nil, fcvm.Paths{}, "test-fc", nil, nil)}, frame.NodeID)
			case "private_error":
				v.err = errors.Join(state.ErrConflict, errors.New(frame.CleanupToken))
			case "bad_proof":
				v.proof.DriveStorageKey = ""
			}
			client := sched.NewVMMClient(qualificationRPCConnection(t, server))
			proof, err := client.CaptureEnvironmentQualification(t.Context(), frame)
			problem := api.AsProblem(err)
			if problem == nil || problem.Code != want || proof != (sched.EnvironmentQualificationSnapshotEvidence{}) || strings.Contains(err.Error(), frame.CleanupToken) {
				t.Fatal("uncertain capture returned proof or exposed authority", err)
			}
		})
	}
}
