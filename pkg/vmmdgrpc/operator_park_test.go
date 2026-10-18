package vmmdgrpc_test

// adr: 1525

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/test/bufconn"
)

type parkReceiptFaultStore struct {
	state.OperatorParkStore
	loseAck, failAck bool
}

func (s *parkReceiptFaultStore) AcknowledgeOperatorPark(ctx context.Context, id, token string, r state.OperatorParkReceipt) error {
	if s.failAck {
		return errors.New("database unavailable")
	}
	if err := s.OperatorParkStore.AcknowledgeOperatorPark(ctx, id, token, r); err != nil {
		return err
	}
	if s.loseAck {
		s.loseAck = false
		return errors.New("receipt commit reply lost")
	}
	return nil
}

type operatorParkNodeVMM struct {
	*fakeVMM
	generation string
	effects    int
	fail       bool
}

func (n *operatorParkNodeVMM) ExecuteOperatorPark(_ context.Context, t state.OperatorParkTarget) (state.OperatorParkReceipt, error) {
	if n.generation != t.Generation {
		return state.OperatorParkReceipt{}, errors.New("generation changed")
	}
	n.effects++
	if n.fail {
		return state.OperatorParkReceipt{}, errors.New("cleanup not confirmed")
	}
	return state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: "capture failed; confirmed cleanup"}, nil
}

func parkWireFixture(t *testing.T) (*state.MemStore, state.OperatorIntent, state.OperatorParkTarget) {
	t.Helper()
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "park@vmmd.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "park", RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForcePark, ins.ID, &a.ID, a.ID, "park", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: ins.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, WakeID: ins.WakeID, Generation: "original-vm"}, InitialState: ins.State, Mode: ins.Mode, StartedAt: ins.StartedAt, LastRequestAt: ins.LastRequestAt, RequestCount: ins.RequestCount, Action: state.OperatorParkStop}
	if _, err := s.PrepareOperatorPark(t.Context(), i.ID, i.ClaimToken, target); err != nil {
		t.Fatal(err)
	}
	return s, i, target
}

func parkWireClient(t *testing.T, n *operatorParkNodeVMM, s state.OperatorParkStore, nodeID string) vmmdpb.VmmdClient {
	t.Helper()
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(peer.NewContext(ctx, &peer.Peer{Addr: &net.UnixAddr{Net: "unix", Name: "vmmd.sock"}}), req)
	}))
	vmmdgrpc.New(n, wire.NewOpsMetrics("vmmd_park_test"), "1.10.0", nil).WithOperatorParkStore(s, nodeID).Register(server)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough://operator-park", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

func TestOperatorParkWireDurableReceipt(t *testing.T) {
	for _, mode := range []string{"normal", "lost_commit_reply", "missing_receipt", "wrong_node", "changed_generation", "cleanup_failure"} {
		t.Run(mode, func(t *testing.T) {
			s, i, target := parkWireFixture(t)
			faults := &parkReceiptFaultStore{OperatorParkStore: s}
			node := &operatorParkNodeVMM{fakeVMM: &fakeVMM{}, generation: target.Generation}
			nodeID := target.NodeID
			switch mode {
			case "lost_commit_reply":
				faults.loseAck = true
			case "missing_receipt":
				faults.failAck = true
			case "wrong_node":
				nodeID = uuid.NewString()
			case "changed_generation":
				node.generation = "replacement"
			case "cleanup_failure":
				node.fail = true
			}
			client := parkWireClient(t, node, faults, nodeID)
			req := &vmmdpb.ExecuteOperatorParkRequest{IntentId: i.ID, ClaimToken: i.ClaimToken}
			response, err := client.ExecuteOperatorPark(t.Context(), req)
			success := mode == "normal" || mode == "lost_commit_reply"
			if success && (err != nil || !response.GetAcknowledged()) {
				t.Fatalf("receipt=%+v %v", response, err)
			}
			if !success && err == nil {
				t.Fatal("unproven operation acknowledged")
			}
			effects := node.effects
			// A fresh daemon with the same durable store must not execute again.
			replacement := parkWireClient(t, node, s, nodeID)
			_, _ = replacement.ExecuteOperatorPark(t.Context(), req)
			if node.effects != effects {
				t.Fatalf("restarted daemon repeated effect: %d -> %d", effects, node.effects)
			}
			e, err := s.GetOperatorPark(t.Context(), i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if success != (e.Receipt != nil) {
				t.Fatalf("proof mismatch: %+v", e)
			}
		})
	}
}

func TestOperatorParkRPCRejectsUnauthorizedPeer(t *testing.T) {
	s, i, target := parkWireFixture(t)
	node := &operatorParkNodeVMM{fakeVMM: &fakeVMM{}, generation: target.Generation}
	server := vmmdgrpc.New(node, wire.NewOpsMetrics("vmmd_park_auth"), "1.10.0", nil).WithOperatorParkStore(s, target.NodeID)
	if _, err := server.ExecuteOperatorPark(t.Context(), &vmmdpb.ExecuteOperatorParkRequest{IntentId: i.ID, ClaimToken: i.ClaimToken}); err == nil || node.effects != 0 {
		t.Fatal("unauthorized peer reached physical effect", err)
	}
}
