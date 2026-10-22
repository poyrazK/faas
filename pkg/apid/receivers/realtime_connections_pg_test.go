package receivers

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAccountConnectionLimitHoldsAcrossNodes runs two realtime nodes against
// one apid and Postgres: an account's allowance is shared between them
// (ADR-1323 fair use).
func TestAccountConnectionLimitHoldsAcrossNodes(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	peer, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "rt-peer-" + uuid.NewString()[:8], TargetURL: "tcp://10.0.0.2:7000",
		VPCPUs: 8, MemMB: 8000, MaxConcurrency: 5, AdmissionCeilingMB: 6000, VCPUBudget: 8, Active: true})
	if err != nil {
		t.Fatal(err)
	}

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterRealtimeFleetReceiver(server, store, nil, nil)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	account, endpoint := uuid.NewString(), uuid.NewString()
	startNode := func(name string) (*realtime.Manager, string) {
		m := realtime.NewManager(realtime.Config{MaxConnections: 100, FairUse: true, ConnectionSync: apidgrpc.NewRealtimeHistoryClientForNode(conn, name),
			ConnectionSyncInterval: 20 * time.Millisecond}, realtime.NopHooks{})
		t.Cleanup(func() { _ = m.Close() })
		if err := m.RegisterEndpoint(realtime.Endpoint{ID: endpoint, AppID: "app", AccountID: account, AccountMaxConnections: 3,
			Authorize: func(context.Context, *http.Request) (string, error) { return "", nil }}); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(m.Handler())
		t.Cleanup(srv.Close)
		return m, "ws" + strings.TrimPrefix(srv.URL, "http") + realtime.ManagedPathPrefix + endpoint
	}
	_, nodeA := startNode(state.DefaultLocalNodeName)
	managerB, nodeB := startNode(peer.Name)
	dial := func(url string) (*websocket.Conn, int, api.Problem) {
		t.Helper()
		ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			t.Cleanup(func() { _ = ws.Close() })
			return ws, http.StatusSwitchingProtocols, api.Problem{}
		}
		if resp == nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		var problem api.Problem
		_ = json.Unmarshal(body, &problem)
		return nil, resp.StatusCode, problem
	}
	// waitReported waits until apid holds node A's count for the account and
	// node B has completed two syncs since, so at least one started after it.
	waitReported := func(want int) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			var got int
			err := pool.QueryRow(ctx, `select coalesce(sum(connections),0) from realtime_node_connections where node_name=$1 and account_id=$2`, state.DefaultLocalNodeName, account).Scan(&got)
			if err == nil && got == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("node A reported %d connections (%v), want %d", got, err, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
		target := managerB.Stats().ConnectionSyncs + 2
		for managerB.Stats().ConnectionSyncs < target {
			if time.Now().After(deadline) {
				t.Fatal("node B did not sync after node A's report")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	first, status, _ := dial(nodeA)
	if status != http.StatusSwitchingProtocols {
		t.Fatalf("node A first connection refused with %d", status)
	}
	if _, status, _ := dial(nodeA); status != http.StatusSwitchingProtocols {
		t.Fatalf("node A second connection refused with %d", status)
	}
	waitReported(2)
	if _, status, _ := dial(nodeB); status != http.StatusSwitchingProtocols {
		t.Fatalf("node B connection within the shared allowance refused with %d", status)
	}
	_, status, problem := dial(nodeB)
	if status != http.StatusTooManyRequests || problem.Code != api.CodeRealtimeConnectionLimit || problem.Observed == nil || *problem.Observed != 3 {
		t.Fatalf("node B over the shared allowance = %d %+v, want 429 observing 2 remote + 1 local", status, problem)
	}

	// A connection closing on node A frees allowance on node B.
	_ = first.Close()
	waitReported(1)
	if _, status, _ := dial(nodeB); status != http.StatusSwitchingProtocols {
		t.Fatalf("node B after a node A close refused with %d", status)
	}
}
