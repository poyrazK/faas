// adr: 568 — portable RPC contract tests; these fixture receipts are not native acceptance.
package sched_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func qualificationRPCFrame() state.EnvironmentQualificationExecution {
	return state.EnvironmentQualificationExecution{InstanceID: uuid.NewString(), RequestID: uuid.NewString(), GraphID: uuid.NewString(), AppID: uuid.NewString(),
		DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(),
		RevisionID: uuid.NewString(), CleanupToken: uuid.NewString(), Resource: "workload/api", Scope: "production", PlanHash: strings.Repeat("a", 64),
		Generation: 5, IntentVersion: 8, Attempt: 2, RAMMB: 512, Artifact: state.EnvironmentWorkloadArtifact{RootfsKey: "apps/original.ext4",
			RootfsPath: "/original.ext4", RootfsBytes: 4096, ImageDigest: "sha256:" + strings.Repeat("b", 64), BuildID: uuid.NewString(),
			Kind: state.DeploymentKindImage, CommitSHA: strings.Repeat("c", 40)}}
}

func qualificationRPCProof() state.EnvironmentQualificationRetirement {
	return state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: uuid.NewString(),
		KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
}

func qualificationRPCConnection(t *testing.T, server vmmdpb.VmmdServer) *grpc.ClientConn {
	t.Helper()
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, server)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://qualification", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

type qualificationRPCVMM struct {
	*fakeVMM
	mu                 sync.Mutex
	created            []state.EnvironmentQualificationExecution
	restored           []state.EnvironmentQualificationExecution
	retired            []state.EnvironmentQualificationExecution
	artifactRetirement struct {
		capture, restored state.EnvironmentQualificationExecution
		smoke             state.EnvironmentQualificationSmokeReceipt
		captureID         string
	}
	wake          fcvm.WakeRequest
	fields        wire.CorrelationFields
	proof         state.EnvironmentQualificationRetirement
	restoreMethod fcvm.WakeMethod
	err           error
}

func (v *qualificationRPCVMM) WakeEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, wake fcvm.WakeRequest) (*fcvm.Instance, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.created = append(v.created, frame)
	v.wake, v.fields = wake, wire.CorrelationFields{}
	v.fields, _ = wire.FromContext(ctx)
	return &fcvm.Instance{Lease: fcvm.Lease{Instance: frame.InstanceID, UID: 20001, HostIP: netip.MustParseAddr("10.100.0.2")},
		Net: netns.Config{Netns: "fc-" + frame.InstanceID}, Method: fcvm.WakeColdBoot}, v.err
}

func (v *qualificationRPCVMM) RetireEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (state.EnvironmentQualificationRetirement, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.retired = append(v.retired, frame)
	return v.proof, v.err
}

func (v *qualificationRPCVMM) RetireEnvironmentQualificationArtifacts(_ context.Context, capture, restored state.EnvironmentQualificationExecution,
	smoke state.EnvironmentQualificationSmokeReceipt, captureID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.artifactRetirement.capture, v.artifactRetirement.restored = capture, restored
	v.artifactRetirement.smoke, v.artifactRetirement.captureID = smoke, captureID
	return v.err
}

func (v *qualificationRPCVMM) RestoreEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, wake fcvm.WakeRequest) (*fcvm.Instance, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.restored = append(v.restored, frame)
	v.wake, v.fields = wake, wire.CorrelationFields{}
	v.fields, _ = wire.FromContext(ctx)
	return &fcvm.Instance{Lease: fcvm.Lease{Instance: frame.InstanceID, UID: 20002, HostIP: netip.MustParseAddr("10.100.0.3")},
		Net: netns.Config{Netns: "fc-" + frame.InstanceID}, Method: v.restoreMethod}, v.err
}

func qualificationRPCServer(v vmmdgrpc.VmmdAPI, nodeID string) *vmmdgrpc.Server {
	return vmmdgrpc.New(v, wire.NewOpsMetrics("qualification_test"), "test-fc", nil).WithNodeID(nodeID)
}

func TestVMMClientEnvironmentQualificationPreservesOriginalRuntimeAndCleanup(t *testing.T) {
	frame := qualificationRPCFrame()
	var generic atomic.Int32
	base := &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
		generic.Add(1)
		return nil, errors.New("generic boot")
	},
		destFn: func(context.Context, string) error { generic.Add(1); return errors.New("generic destroy") }}
	v := &qualificationRPCVMM{fakeVMM: base, proof: qualificationRPCProof()}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	app := sched.AppSpec{AppID: frame.AppID, DeploymentID: frame.DeploymentID, AccountID: uuid.NewString(), Plan: api.PlanPro,
		BaseKey: "base/node22.ext4", LayerKey: frame.Artifact.RootfsKey, VCPUCount: 2, MemSizeMiB: 512,
		APIEnv: []fcvm.APIEnvEntry{{Key: "MODE", Value: "reviewed"}}, SealedEnv: []fcvm.SealedEnvEntry{{Key: "TOKEN", Ciphertext: []byte("sealed")}}}
	out, err := client.CreateEnvironmentQualification(ctx, frame, app)
	cancel()
	if err != nil || out == nil || out.Instance != frame.InstanceID {
		t.Fatal("private runtime was not acknowledged", err)
	}
	evidence, err := client.RetireEnvironmentQualification(t.Context(), frame)
	if err != nil || evidence.Execution != frame || evidence.Retirement != v.proof {
		t.Fatal("cleanup changed original frame or physical evidence", err)
	}
	restoredFrame := frame
	restoredFrame.InstanceID, restoredFrame.WakeID, restoredFrame.CaptureInstanceID = uuid.NewString(), uuid.NewString(), frame.InstanceID
	restoredRetirement, err := client.RetireEnvironmentQualification(t.Context(), restoredFrame)
	if err != nil || restoredRetirement.Execution != restoredFrame || restoredRetirement.Retirement != v.proof {
		t.Fatal("cleanup lost the separate restore target authority", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.created) != 1 || v.created[0] != frame || len(v.retired) != 2 || v.retired[0] != frame || v.retired[1] != restoredFrame || generic.Load() != 0 {
		t.Fatal("qualification used generic operations or changed the complete original attempt")
	}
	if v.fields.WakeID != frame.WakeID || v.fields.DeploymentID != frame.DeploymentID || v.fields.InstanceID != frame.InstanceID ||
		v.fields.AppID != frame.AppID || v.fields.NodeID != frame.NodeID || v.wake.DeploymentID != frame.DeploymentID || len(v.wake.SealedEnvEntries) != 1 ||
		len(v.wake.APIEnvEntries) != 1 || v.wake.APIEnvEntries[0].Value != "reviewed" || v.wake.LayerKey != frame.Artifact.RootfsKey {
		t.Fatal("host lost frozen boot payload or correlation")
	}
}

func TestVMMClientEnvironmentQualificationRestoreUsesSeparateCaptureAndNoFallback(t *testing.T) {
	frame := qualificationRPCFrame()
	frame.InstanceID = uuid.NewString()
	frame.WakeID = uuid.NewString()
	frame.CaptureInstanceID = qualificationRPCFrame().InstanceID
	var generic atomic.Int32
	base := &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
		generic.Add(1)
		return &fcvm.Instance{Method: fcvm.WakeColdBoot}, nil
	}}
	v := &qualificationRPCVMM{fakeVMM: base, restoreMethod: fcvm.WakeRestore}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
	app := sched.AppSpec{AppID: frame.AppID, DeploymentID: frame.DeploymentID, AccountID: uuid.NewString(), Plan: api.PlanPro,
		BaseKey: "base/node22.ext4", LayerKey: frame.Artifact.RootfsKey, VCPUCount: 2, MemSizeMiB: 512,
		APIEnv: []fcvm.APIEnvEntry{{Key: "MODE", Value: "reviewed"}}}
	out, err := client.RestoreEnvironmentQualification(t.Context(), frame, app)
	if err != nil || out == nil || out.Method != vmmdpb.WakeMethod_WAKE_RESTORE || out.Instance != frame.InstanceID {
		t.Fatal("restore was not acknowledged as an exact native restore", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.restored) != 1 || v.restored[0] != frame || len(v.created) != 0 || generic.Load() != 0 ||
		v.fields.InstanceID != frame.InstanceID || v.wake.LayerKey != frame.Artifact.RootfsKey || len(v.wake.APIEnvEntries) != 1 {
		t.Fatal("restore changed target/capture authority or borrowed generic wake", v.restored, v.fields, generic.Load())
	}
}

func TestEnvironmentQualificationRestoreRejectsColdBootFallback(t *testing.T) {
	frame := qualificationRPCFrame()
	frame.InstanceID = uuid.NewString()
	frame.WakeID = uuid.NewString()
	frame.CaptureInstanceID = qualificationRPCFrame().InstanceID
	var generic atomic.Int32
	v := &qualificationRPCVMM{fakeVMM: &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
		generic.Add(1)
		return nil, nil
	}}}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
	app := sched.AppSpec{AppID: frame.AppID, DeploymentID: frame.DeploymentID, AccountID: uuid.NewString(), Plan: api.PlanPro,
		BaseKey: "base/node22.ext4", LayerKey: frame.Artifact.RootfsKey, VCPUCount: 2, MemSizeMiB: 512}
	if out, err := client.RestoreEnvironmentQualification(t.Context(), frame, app); err == nil || out != nil || generic.Load() != 0 {
		t.Fatal("cold-boot fallback was accepted as a restore", out, err, generic.Load())
	}
}

func TestVMMClientEnvironmentQualificationArtifactRetirementCarriesOwnerEvidence(t *testing.T) {
	capture := qualificationRPCFrame()
	restored := capture
	restored.InstanceID, restored.WakeID, restored.CleanupToken = uuid.NewString(), uuid.NewString(), uuid.NewString()
	restored.CaptureInstanceID = capture.InstanceID
	smoke := state.EnvironmentQualificationSmokeReceipt{RequestID: capture.RequestID, Attempt: capture.Attempt, GraphID: capture.GraphID,
		CaptureInstanceID: capture.InstanceID, InstanceID: restored.InstanceID, Resource: capture.Resource, PolicyID: "http-healthz-v1",
		PolicySHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("b", 64), RecordedAt: time.UnixMilli(time.Now().UnixMilli()).UTC()}
	captureID := uuid.NewString()
	v := &qualificationRPCVMM{fakeVMM: &fakeVMM{}}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, capture.NodeID)))
	if err := client.RetireEnvironmentQualificationArtifacts(t.Context(), capture, restored, smoke, captureID); err != nil {
		t.Fatal("host did not acknowledge exact artifact retirement", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.artifactRetirement.capture != capture || v.artifactRetirement.restored != restored ||
		v.artifactRetirement.smoke != smoke || v.artifactRetirement.captureID != captureID {
		t.Fatal("artifact retirement did not preserve the exact owner evidence", v.artifactRetirement)
	}
}

func qualificationRPCMetadata(ctx context.Context, f state.EnvironmentQualificationExecution) context.Context {
	return wire.WithCorrelationOutgoing(ctx, wire.CorrelationFields{WakeID: f.WakeID, AppID: f.AppID, DeploymentID: f.DeploymentID, InstanceID: f.InstanceID, NodeID: f.NodeID})
}

func TestEnvironmentQualificationRPCRejectsChangedEnvelopeBeforeEffects(t *testing.T) {
	for _, failure := range []string{"nil", "version", "node", "memory", "app", "artifact", "account", "plan", "path_only", "metadata", "duplicate_metadata", "unknown"} {
		t.Run(failure, func(t *testing.T) {
			frame := qualificationRPCFrame()
			v := &qualificationRPCVMM{fakeVMM: &fakeVMM{}, proof: qualificationRPCProof()}
			client := vmmdpb.NewVmmdClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
			encoded, err := qualificationwire.ExecutionToProto(frame)
			if err != nil {
				t.Fatal(err)
			}
			req := &vmmdpb.CreateEnvironmentQualificationRequest{Execution: encoded, App: &vmmdpb.AppSpec{AppId: frame.AppID, BaseKey: "base/node.ext4",
				LayerKey: frame.Artifact.RootfsKey, VcpuCount: 2, MemSizeMib: 512}, Plan: string(api.PlanPro), AccountId: uuid.NewString()}
			ctx := qualificationRPCMetadata(t.Context(), frame)
			want := codes.InvalidArgument
			switch failure {
			case "nil":
				req = nil
			case "version":
				req.Execution.ContractVersion = 0
			case "node":
				req.Execution.NodeId = uuid.NewString()
				want = codes.FailedPrecondition
			case "memory":
				req.App.MemSizeMib++
			case "app":
				req.App.AppId = uuid.NewString()
			case "artifact":
				req.App.LayerKey = "replacement.ext4"
			case "account":
				req.AccountId = ""
			case "plan":
				req.Plan = "unknown"
			case "path_only":
				req.Execution.Artifact.RootfsKey = ""
			case "metadata":
				changed := frame
				changed.WakeID = uuid.NewString()
				ctx = qualificationRPCMetadata(t.Context(), changed)
			case "duplicate_metadata":
				ctx = metadata.AppendToOutgoingContext(ctx, "x-faas-wake-id", frame.WakeID)
			case "unknown":
				req.Execution.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			}
			_, err = client.CreateEnvironmentQualification(ctx, req)
			if status.Code(err) != want {
				t.Fatal("invalid envelope returned wrong status", err)
			}
			v.mu.Lock()
			defer v.mu.Unlock()
			if len(v.created) != 0 || len(v.retired) != 0 {
				t.Fatal("rejected envelope reached native owner")
			}
		})
	}
}

func TestEnvironmentQualificationRPCKeepsUnconfirmedRetirementPrivate(t *testing.T) {
	for _, failure := range []string{"missing_resources", "never_dispatched", "uncertain", "private_error"} {
		t.Run(failure, func(t *testing.T) {
			frame := qualificationRPCFrame()
			v := &qualificationRPCVMM{fakeVMM: &fakeVMM{}, proof: qualificationRPCProof()}
			want := api.CodeEnvironmentQualificationUnconfirmed
			switch failure {
			case "missing_resources":
				v.proof.ResourcesRemoved = false
			case "never_dispatched":
				v.proof = state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}
			case "uncertain":
				v.err = state.ErrConflict
			case "private_error":
				v.err = errors.New("private journal " + frame.CleanupToken)
				want = api.CodeInternal
			}
			client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
			evidence, err := client.RetireEnvironmentQualification(t.Context(), frame)
			problem := api.AsProblem(err)
			if err == nil || evidence != (sched.EnvironmentQualificationRetirementEvidence{}) || problem == nil || problem.Code != want || strings.Contains(err.Error(), frame.CleanupToken) {
				t.Fatal("uncertainty exposed authority or acknowledged retirement")
			}
			if failure != "private_error" && !errors.Is(err, state.ErrConflict) {
				t.Fatal("typed conflict fence was lost", err)
			}
		})
	}
}

type qualificationReplyServer struct {
	vmmdpb.UnimplementedVmmdServer
	proof        *vmmdpb.EnvironmentQualificationRetirement
	changeCreate func(*vmmdpb.CreateEnvironmentQualificationResponse)
	changeRetire func(*vmmdpb.RetireEnvironmentQualificationResponse)
	generic      atomic.Int32
}

func (s *qualificationReplyServer) CreateEnvironmentQualification(_ context.Context, req *vmmdpb.CreateEnvironmentQualificationRequest) (*vmmdpb.CreateEnvironmentQualificationResponse, error) {
	r := &vmmdpb.CreateEnvironmentQualificationResponse{Execution: proto.Clone(req.GetExecution()).(*vmmdpb.EnvironmentQualificationExecution),
		Wake: &vmmdpb.WakeResponse{Instance: req.GetExecution().GetInstanceId(), Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT,
			RequestedMethod: vmmdpb.WakeMethod_WAKE_COLD_BOOT, SupportsSecretAliases: true}}
	if s.changeCreate != nil {
		s.changeCreate(r)
	}
	return r, nil
}

func (s *qualificationReplyServer) RetireEnvironmentQualification(_ context.Context, req *vmmdpb.RetireEnvironmentQualificationRequest) (*vmmdpb.RetireEnvironmentQualificationResponse, error) {
	r := &vmmdpb.RetireEnvironmentQualificationResponse{Execution: proto.Clone(req.GetExecution()).(*vmmdpb.EnvironmentQualificationExecution),
		Retirement: proto.Clone(s.proof).(*vmmdpb.EnvironmentQualificationRetirement)}
	if s.changeRetire != nil {
		s.changeRetire(r)
	}
	return r, nil
}

func (s *qualificationReplyServer) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{SupportsSecretAliases: true}, nil
}

func (s *qualificationReplyServer) CreateColdBoot(context.Context, *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	s.generic.Add(1)
	return nil, status.Error(codes.Unimplemented, "generic boot")
}

func (s *qualificationReplyServer) Destroy(context.Context, *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	s.generic.Add(1)
	return &vmmdpb.DestroyResponse{}, nil
}

func TestVMMClientEnvironmentQualificationRefusesSubstitutedReplies(t *testing.T) {
	for _, failure := range []string{"node", "cleanup", "attempt", "artifact", "version", "empty_frame", "empty_proof", "exit", "never_dispatched", "unknown_proof"} {
		t.Run(failure, func(t *testing.T) {
			frame := qualificationRPCFrame()
			proof, err := qualificationwire.NativeRetirementToProto(qualificationRPCProof())
			if err != nil {
				t.Fatal(err)
			}
			s := &qualificationReplyServer{proof: proof, changeRetire: func(r *vmmdpb.RetireEnvironmentQualificationResponse) {
				switch failure {
				case "node":
					r.Execution.NodeId = uuid.NewString()
				case "cleanup":
					r.Execution.CleanupToken = uuid.NewString()
				case "attempt":
					r.Execution.Attempt++
				case "artifact":
					r.Execution.Artifact.RootfsBytes++
				case "version":
					r.Execution.ContractVersion++
				case "empty_frame":
					r.Execution = nil
				case "empty_proof":
					r.Retirement = nil
				case "exit":
					r.Retirement.ProcessesExited = false
				case "never_dispatched":
					r.Retirement.Kind = state.QualificationNeverDispatched
				case "unknown_proof":
					r.Retirement.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
				}
			}}
			client := sched.NewVMMClient(qualificationRPCConnection(t, s))
			evidence, err := client.RetireEnvironmentQualification(t.Context(), frame)
			if !errors.Is(err, state.ErrConflict) || evidence != (sched.EnvironmentQualificationRetirementEvidence{}) || s.generic.Load() != 0 {
				t.Fatal("substituted reply supplied retirement or generic cleanup", err)
			}
		})
	}
	for _, failure := range []string{"instance", "frame", "method", "missing_alias_ack"} {
		t.Run("create_"+failure, func(t *testing.T) {
			frame := qualificationRPCFrame()
			s := &qualificationReplyServer{changeCreate: func(r *vmmdpb.CreateEnvironmentQualificationResponse) {
				switch failure {
				case "instance":
					r.Wake.Instance = uuid.NewString()
				case "frame":
					r.Execution.DeploymentId = uuid.NewString()
				case "method":
					r.Wake.Method = vmmdpb.WakeMethod_WAKE_RESTORE
				case "missing_alias_ack":
					r.Wake.SupportsSecretAliases = false
				}
			}}
			client := sched.NewVMMClient(qualificationRPCConnection(t, s))
			app := sched.AppSpec{}
			if failure == "missing_alias_ack" {
				app.SealedEnv = []fcvm.SealedEnvEntry{{Key: "TARGET", SourceKey: "SOURCE", Ciphertext: []byte("sealed")}}
			}
			out, err := client.CreateEnvironmentQualification(t.Context(), frame, app)
			if !errors.Is(err, state.ErrConflict) || out != nil || s.generic.Load() != 0 {
				t.Fatal("unconfirmed boot published runtime or used generic cleanup", err)
			}
		})
	}
}

func TestVMMClientEnvironmentQualificationOlderNodesNeverFallback(t *testing.T) {
	frame := qualificationRPCFrame()
	var generic atomic.Int32
	v := &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) { generic.Add(1); return nil, nil },
		destFn: func(context.Context, string) error { generic.Add(1); return nil }}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
	_, bootErr := client.CreateEnvironmentQualification(t.Context(), frame, sched.AppSpec{})
	evidence, retireErr := client.RetireEnvironmentQualification(t.Context(), frame)
	for _, err := range []error{bootErr, retireErr} {
		p := api.AsProblem(err)
		if p == nil || p.Code != api.CodeNotImplemented {
			t.Fatal("older node did not refuse missing native capability", err)
		}
	}
	if generic.Load() != 0 || evidence != (sched.EnvironmentQualificationRetirementEvidence{}) {
		t.Fatal("older node fell back to generic boot/destroy")
	}
}

// Match cmd/vmmd's signalAdapter while preserving the actual Manager methods.
type qualificationManagerRPC struct{ *fcvm.Manager }

func (m qualificationManagerRPC) SignalAndKill(ctx context.Context, instance string, signal, graceSeconds int32) (bool, int32, error) {
	return m.Manager.SignalAndKill(ctx, instance, syscall.Signal(signal), time.Duration(graceSeconds)*time.Second)
}

func TestEnvironmentQualificationRPCRealManagerRemainsDisabled(t *testing.T) {
	frame := qualificationRPCFrame()
	// Actual Manager, with no native node identity or host backend. Neither
	// RPC may allocate or manufacture proof through its generic surface.
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test-fc", nil, nil)
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(qualificationManagerRPC{mgr}, frame.NodeID)))
	app := sched.AppSpec{AppID: frame.AppID, AccountID: uuid.NewString(), Plan: api.PlanPro, BaseKey: "base/node22.ext4",
		LayerKey: frame.Artifact.RootfsKey, VCPUCount: 2, MemSizeMiB: int32(frame.RAMMB)}
	out, bootErr := client.CreateEnvironmentQualification(t.Context(), frame, app)
	evidence, retireErr := client.RetireEnvironmentQualification(t.Context(), frame)
	if !errors.Is(bootErr, state.ErrConflict) || !errors.Is(retireErr, state.ErrConflict) || out != nil ||
		evidence != (sched.EnvironmentQualificationRetirementEvidence{}) || mgr.LiveCount() != 0 || mgr.LeasedCount() != 0 {
		t.Fatal("production-default Manager enabled qualification or supplied cleanup proof")
	}
}

func TestVMMClientEnvironmentQualificationRejectsConflictingCorrelation(t *testing.T) {
	frame := qualificationRPCFrame()
	v := &qualificationRPCVMM{fakeVMM: &fakeVMM{}, proof: qualificationRPCProof()}
	client := sched.NewVMMClient(qualificationRPCConnection(t, qualificationRPCServer(v, frame.NodeID)))
	for _, outgoing := range []bool{false, true} {
		fields := wire.CorrelationFields{WakeID: uuid.NewString()}
		ctx := wire.WithContext(t.Context(), fields)
		if outgoing {
			ctx = wire.WithCorrelationOutgoing(t.Context(), fields)
		}
		if _, err := client.RetireEnvironmentQualification(ctx, frame); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("conflicting caller identity was silently replaced", err)
		}
	}
	v.mu.Lock()
	retired := len(v.retired)
	v.mu.Unlock()
	if retired != 0 {
		t.Fatal("conflicting correlation reached native effects")
	}
	// Repeated, matching caller metadata is normalized before dispatch, so it
	// remains single-valued at the strict receiving boundary.
	ctx := qualificationRPCMetadata(qualificationRPCMetadata(t.Context(), frame), frame)
	evidence, err := client.RetireEnvironmentQualification(ctx, frame)
	if err != nil || evidence.Execution != frame {
		t.Fatal("matching correlation could not dispatch once", err)
	}
}
