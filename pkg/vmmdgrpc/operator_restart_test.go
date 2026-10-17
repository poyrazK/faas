package vmmdgrpc_test

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
	"google.golang.org/grpc/test/bufconn"
)

type restartReceiptFaultStore struct {
	state.OperatorRestartStore
	loseAcknowledgement bool
	failAcknowledgement bool
}

func (s *restartReceiptFaultStore) AcknowledgeOperatorRestartDestroy(ctx context.Context, id, token string) error {
	if s.failAcknowledgement {
		return errors.New("database unavailable")
	}
	if err := s.OperatorRestartStore.AcknowledgeOperatorRestartDestroy(ctx, id, token); err != nil {
		return err
	}
	if s.loseAcknowledgement {
		s.loseAcknowledgement = false
		return errors.New("database acknowledgement lost")
	}
	return nil
}

type operatorRestartNode struct {
	*fakeVMM
	generation string
	destroys   int
}

func (n *operatorRestartNode) OperatorRestartGeneration(string) (string, error) {
	return n.generation, nil
}
func (n *operatorRestartNode) DestroyOperatorRestart(_ context.Context, _, generation string) error {
	if generation != n.generation {
		return errors.New("physical VM generation changed")
	}
	n.destroys++
	return nil
}

func restartWireClient(t *testing.T, node *operatorRestartNode, store state.OperatorRestartStore, nodeID string) vmmdpb.VmmdClient {
	t.Helper()
	server := grpc.NewServer()
	vmmdgrpc.New(node, wire.NewOpsMetrics("vmmd_restart_test"), "1.10.0", nil).WithOperatorRestartStore(store, nodeID).Register(server)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough://operator-restart", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

func restartWireFixture(t *testing.T) (*state.MemStore, state.OperatorIntent, state.OperatorRestartTarget) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "restart@vmmd.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "restart", RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceRestart, instance.ID, &account.ID, account.ID, "restart", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	target := state.OperatorRestartTarget{InstanceID: instance.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, WakeID: instance.WakeID, Generation: "original-physical-vm"}
	if _, err := store.PrepareOperatorRestart(t.Context(), intent.ID, intent.ClaimToken, target); err != nil {
		t.Fatal(err)
	}
	return store, intent, target
}

func TestOperatorRestartWireDurableAcknowledgement(t *testing.T) {
	for _, mode := range []string{"normal", "lost_database_acknowledgement", "lost_receipt", "wrong_node", "changed_generation"} {
		t.Run(mode, func(t *testing.T) {
			store, intent, target := restartWireFixture(t)
			receipts := &restartReceiptFaultStore{OperatorRestartStore: store}
			node := &operatorRestartNode{fakeVMM: &fakeVMM{}, generation: target.Generation}
			nodeID := target.NodeID
			switch mode {
			case "lost_database_acknowledgement":
				receipts.loseAcknowledgement = true
			case "lost_receipt":
				receipts.failAcknowledgement = true
			case "wrong_node":
				nodeID = uuid.NewString()
			case "changed_generation":
				node.generation = "replacement-physical-vm"
			}
			client := restartWireClient(t, node, receipts, nodeID)
			request := &vmmdpb.ExecuteOperatorRestartDestroyRequest{IntentId: intent.ID, ClaimToken: intent.ClaimToken}
			response, err := client.ExecuteOperatorRestartDestroy(t.Context(), request)
			wantSuccess := mode == "normal" || mode == "lost_database_acknowledgement"
			if wantSuccess && (err != nil || !response.GetAcknowledged()) {
				t.Fatalf("execute = %+v, %v", response, err)
			}
			if !wantSuccess && err == nil {
				t.Fatalf("uncertain or invalid action acknowledged: %+v", response)
			}
			wantEffects := 1
			if mode == "wrong_node" || mode == "changed_generation" {
				wantEffects = 0
			}
			if node.destroys != wantEffects {
				t.Fatalf("physical effects=%d, want %d", node.destroys, wantEffects)
			}
			if mode != "wrong_node" {
				// Simulate a fresh vmmd process with no in-memory execution history.
				fresh := &operatorRestartNode{fakeVMM: &fakeVMM{}, generation: "new-daemon-generation"}
				recovery := restartWireClient(t, fresh, store, target.NodeID)
				again, err := recovery.ExecuteOperatorRestartDestroy(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				if fresh.destroys != 0 || again.GetAcknowledged() != wantSuccess {
					t.Fatalf("recovery repeated or invented effect: %+v, effects=%d", again, fresh.destroys)
				}
			}
		})
	}
}
