package sched_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/postgresdrainwire"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type drainRPCAuthority func(context.Context, state.ManagedPostgresDrainFrame, func(context.Context) (state.ManagedPostgresDrainProof, error)) (state.ManagedPostgresDrainProof, error)

func (f drainRPCAuthority) WithManagedPostgresDrainAuthority(ctx context.Context, frame state.ManagedPostgresDrainFrame, retire func(context.Context) (state.ManagedPostgresDrainProof, error)) (state.ManagedPostgresDrainProof, error) {
	return f(ctx, frame, retire)
}

type drainRPCVMM struct {
	vmmdgrpc.VmmdAPI
	proof state.ManagedPostgresDrainProof
	calls int
}

func (v *drainRPCVMM) RetireManagedPostgresDrain(context.Context, state.ManagedPostgresDrainFrame) (state.ManagedPostgresDrainProof, error) {
	v.calls++
	return v.proof, nil
}
func drainRPCFrame() state.ManagedPostgresDrainFrame {
	return state.ManagedPostgresDrainFrame{ID: uuid.NewString(), CutoverID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), OwnerNodeID: uuid.NewString(), OwnerRevision: 1, FencedAt: time.Now().UTC()}
}

func TestManagedPostgresDrainRPCRequiresAuthorityAndCompleteNativeProof(t *testing.T) {
	for _, failure := range []string{"none", "no_authority", "stale_authority", "cleanup", "metadata_conflict", "wrong_node", "legacy_vmm"} {
		t.Run(failure, func(t *testing.T) {
			f := drainRPCFrame()
			v := &drainRPCVMM{proof: state.ManagedPostgresDrainProof{DrainID: f.ID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}}
			s := postgresDrainRPCServer(v, f.NodeID)
			s.WithManagedPostgresDrainAuthority(drainRPCAuthority(func(ctx context.Context, seen state.ManagedPostgresDrainFrame, retire func(context.Context) (state.ManagedPostgresDrainProof, error)) (state.ManagedPostgresDrainProof, error) {
				if !seen.Equal(f) {
					t.Error("wire changed authority")
				}
				if failure == "stale_authority" {
					return state.ManagedPostgresDrainProof{}, state.ErrConflict
				}
				return retire(ctx)
			}))
			ctx := t.Context()
			switch failure {
			case "no_authority":
				s.WithManagedPostgresDrainAuthority(nil)
			case "cleanup":
				v.proof.ResourcesRemoved = false
			case "metadata_conflict":
				ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-faas-node-id", uuid.NewString()))
			case "wrong_node":
				s.WithNodeID(uuid.NewString())
			case "legacy_vmm":
				s = postgresDrainRPCServer(nil, f.NodeID)
			}
			client := sched.NewVMMClient(postgresDrainRPCConnection(t, s))
			evidence, err := client.RetireManagedPostgresDrain(ctx, f)
			if failure == "none" {
				if err != nil || !evidence.Frame.Equal(f) || evidence.Proof != v.proof || v.calls != 1 {
					t.Fatal("RPC lost receipt", evidence, err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsupported or uncertain retirement acknowledged")
			}
			if failure != "cleanup" && v.calls != 0 {
				t.Fatal("invalid authority reached physical stop")
			}
			if failure == "metadata_conflict" && !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal(err)
			}
			if failure == "cleanup" && status.Code(err) != codes.FailedPrecondition {
				t.Fatal(err)
			}
		})
	}
}

type forgedDrainServer struct {
	vmmdpb.UnimplementedVmmdServer
	response *vmmdpb.RetireManagedPostgresDrainResponse
}

func (s forgedDrainServer) RetireManagedPostgresDrain(context.Context, *vmmdpb.RetireManagedPostgresDrainRequest) (*vmmdpb.RetireManagedPostgresDrainResponse, error) {
	return s.response, nil
}
func TestManagedPostgresDrainClientRejectsSubstitutedOrIncompleteReply(t *testing.T) {
	for _, failure := range []string{"wake", "native_generation", "resources", "unknown"} {
		t.Run(failure, func(t *testing.T) {
			f := drainRPCFrame()
			encoded, err := postgresdrainwire.FrameToProto(f)
			if err != nil {
				t.Fatal(err)
			}
			p := &vmmdpb.ManagedPostgresDrainProof{DrainId: f.ID, NativeGeneration: uuid.NewString(), KernelBootId: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
			switch failure {
			case "wake":
				encoded.WakeId = uuid.NewString()
			case "native_generation":
				p.NativeGeneration = ""
			case "resources":
				p.ResourcesRemoved = false
			case "unknown":
				p.ProtoReflect().SetUnknown([]byte{0x78, 0x01})
			}
			client := sched.NewVMMClient(postgresDrainRPCConnection(t, forgedDrainServer{response: &vmmdpb.RetireManagedPostgresDrainResponse{Frame: encoded, Proof: p}}))
			if _, err := client.RetireManagedPostgresDrain(t.Context(), f); !errors.Is(err, state.ErrConflict) {
				t.Fatal("forged reply accepted", err)
			}
		})
	}
}

func postgresDrainRPCServer(v vmmdgrpc.VmmdAPI, node string) *vmmdgrpc.Server {
	return vmmdgrpc.New(v, wire.NewOpsMetrics("postgres_drain_test"), "test-fc", nil).WithNodeID(node)
}
func postgresDrainRPCConnection(t *testing.T, server vmmdpb.VmmdServer) *grpc.ClientConn {
	t.Helper()
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, server)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := wire.DialContext(t.Context(), "unix:///postgres-drain", nil, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
