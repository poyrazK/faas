package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeRealtimeHistoryStore struct {
	endpoint state.ManagedRealtimeEndpoint
	history  state.ManagedRealtimeChannelHistory
	err      error
}

func (f *fakeRealtimeHistoryStore) ManagedRealtimeEndpointByID(_ context.Context, id string) (state.ManagedRealtimeEndpoint, error) {
	if id != f.endpoint.ID {
		return state.ManagedRealtimeEndpoint{}, state.ErrNotFound
	}
	return f.endpoint, nil
}

func (f *fakeRealtimeHistoryStore) AppendManagedRealtimeChannelMessage(context.Context, string, string, []byte, bool, string) (state.ManagedRealtimeChannelMessage, error) {
	panic("read-only RPC must not append")
}

func (f *fakeRealtimeHistoryStore) ReadManagedRealtimeChannelHistory(_ context.Context, endpointID, channel string, after int64, limit int) (state.ManagedRealtimeChannelHistory, error) {
	if endpointID != f.endpoint.ID || channel != "updates" || after != 812 || limit != 100 {
		panic("unexpected history query")
	}
	return f.history, f.err
}

func TestRealtimeHistoryRPC(t *testing.T) {
	stamp := time.Date(2026, time.September, 28, 12, 0, 0, 123, time.UTC)
	store := &fakeRealtimeHistoryStore{
		endpoint: state.ManagedRealtimeEndpoint{ID: "endpoint-1", Enabled: true},
		history: state.ManagedRealtimeChannelHistory{
			OldestSequence: 810, LatestSequence: 820,
			Messages: []state.ManagedRealtimeChannelMessage{{Sequence: 813, Data: []byte("hello"), Binary: true, CreatedAt: stamp}},
		},
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	apidpb.RegisterRealtimeHistoryServer(server, &realtimeHistoryReceiver{store: store})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx := context.Background()
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := apidgrpc.NewRealtimeHistoryClient(conn)
	page, err := client.ReadChannelHistory(ctx, "endpoint-1", "updates", 812, 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.OldestSequence != 810 || page.LatestSequence != 820 || len(page.Messages) != 1 ||
		page.Messages[0].Sequence != 813 || string(page.Messages[0].Data) != "hello" ||
		!page.Messages[0].Binary || !page.Messages[0].CreatedAt.Equal(stamp) {
		t.Fatalf("round trip page = %+v", page)
	}
	store.history = state.ManagedRealtimeChannelHistory{OldestSequence: 815, LatestSequence: 820, HistoryUnavailable: true}
	page, err = client.ReadChannelHistory(ctx, "endpoint-1", "updates", 812, 100)
	if err != nil || !page.HistoryUnavailable || len(page.Messages) != 0 || page.OldestSequence != 815 {
		t.Fatalf("expired cursor page = %+v, err = %v", page, err)
	}
	store.endpoint.Enabled = false
	_, err = client.ReadChannelHistory(ctx, "endpoint-1", "updates", 812, 100)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled endpoint error = %v", err)
	}
	store.endpoint.Enabled = true
	_, err = client.ReadChannelHistory(ctx, "other-endpoint", "updates", 812, 100)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown endpoint error = %v", err)
	}
	_, err = client.ReadChannelHistory(ctx, "endpoint-1", "updates", -1, 100)
	if !errors.Is(err, state.ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("local invalid cursor error = %v", err)
	}
	_, err = apidpb.NewRealtimeHistoryClient(conn).ReadChannelHistory(ctx, &apidpb.ReadChannelHistoryRequest{EndpointId: "endpoint-1", Channel: "updates", AfterSequence: -1, Limit: 100})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("server invalid cursor error = %v", err)
	}
}

func newRealtimeRouteReportFixture(t *testing.T) (*state.MemStore, state.ManagedRealtimeEndpoint, state.ComputeNode) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "route-report@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create route report account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "route-report", Status: state.AppActive, RAMMB: 512})
	if err != nil {
		t.Fatalf("create route report app: %v", err)
	}
	endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		AccountID: account.ID, AppID: app.ID, CallbackURL: "https://example.com/callback", Enabled: true,
	}, 10, 10)
	if err != nil {
		t.Fatalf("create route report endpoint: %v", err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("find default realtime node: %v", err)
	}
	return store, endpoint, node
}

func newRealtimeHistoryTestClient(t *testing.T, store state.Store, nodeName string) *apidgrpc.RealtimeHistoryClientImpl {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	registerRealtimeHistoryReceiver(server, store)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial realtime history test server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return apidgrpc.NewRealtimeHistoryClientForNode(conn, nodeName)
}

func TestReportChannelRouteRPCAddsAndRemovesNodeTarget(t *testing.T) {
	ctx := context.Background()
	store, endpoint, node := newRealtimeRouteReportFixture(t)
	client := newRealtimeHistoryTestClient(t, store, node.Name)

	if err := client.ReportChannelRoute(ctx, endpoint.ID, "updates", true); err != nil {
		t.Fatalf("report route add: %v", err)
	}
	view, err := store.ListManagedRealtimeChannelRouteView(ctx, endpoint.ID, "updates")
	if err != nil {
		t.Fatalf("list route view after add: %v", err)
	}
	if len(view.NodeIDs) != 1 || view.NodeIDs[0] != node.ID {
		t.Fatalf("route nodes after add = %v, want [%s]", view.NodeIDs, node.ID)
	}

	if err := client.ReportChannelRoute(ctx, endpoint.ID, "updates", false); err != nil {
		t.Fatalf("report route removal: %v", err)
	}
	view, err = store.ListManagedRealtimeChannelRouteView(ctx, endpoint.ID, "updates")
	if err != nil {
		t.Fatalf("list route view after removal: %v", err)
	}
	if len(view.NodeIDs) != 0 {
		t.Fatalf("route nodes after removal = %v, want empty", view.NodeIDs)
	}
}

type observedRealtimeRouteStore struct {
	state.ManagedRealtimeChannelRouteStore
	acquireAttempted chan struct{}
}

func (s observedRealtimeRouteStore) AcquireManagedRealtimeChannelRouteLock(ctx context.Context, nodeID string) (state.ManagedRealtimeChannelRouteLock, error) {
	select {
	case s.acquireAttempted <- struct{}{}:
	default:
	}
	return s.ManagedRealtimeChannelRouteStore.AcquireManagedRealtimeChannelRouteLock(ctx, nodeID)
}

func TestReportChannelRouteWaitsForSnapshotLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store, endpoint, node := newRealtimeRouteReportFixture(t)
	observedRoutes := observedRealtimeRouteStore{ManagedRealtimeChannelRouteStore: store, acquireAttempted: make(chan struct{}, 1)}
	receiver := &realtimeHistoryReceiver{store: store, nodes: store, routes: observedRoutes, remover: store}

	// Hold the node lock as a snapshot would. A route report that arrives while
	// that snapshot is replacing a stale empty view must wait and apply after it.
	lock, err := store.AcquireManagedRealtimeChannelRouteLock(ctx, node.ID)
	if err != nil {
		t.Fatalf("acquire route snapshot lock: %v", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			lock.Release(context.Background())
		}
	}()
	reported := make(chan error, 1)
	go func() {
		response, reportErr := receiver.ReportChannelRoute(ctx, &apidpb.ReportChannelRouteRequest{
			EndpointId: endpoint.ID, Channel: "updates", NodeName: node.Name, Subscribed: true,
		})
		if reportErr == nil && !response.GetApplied() {
			reportErr = errors.New("route report was not applied")
		}
		reported <- reportErr
	}()
	select {
	case <-observedRoutes.acquireAttempted:
	case <-ctx.Done():
		t.Fatal("route report did not attempt to acquire the snapshot lock")
	}
	select {
	case err := <-reported:
		t.Fatalf("route report passed a held snapshot lock: %v", err)
	default:
	}

	generation, err := store.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		t.Fatalf("read route generation: %v", err)
	}
	if err := store.ReplaceManagedRealtimeChannelRoutes(ctx, node.ID, generation, nil); err != nil {
		t.Fatalf("replace stale snapshot: %v", err)
	}
	lock.Release(ctx)
	lockReleased = true
	select {
	case err := <-reported:
		if err != nil {
			t.Fatalf("apply route report after snapshot: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("route report did not finish after snapshot released its lock")
	}
	view, err := store.ListManagedRealtimeChannelRouteView(ctx, endpoint.ID, "updates")
	if err != nil {
		t.Fatalf("list route view after concurrent snapshot and report: %v", err)
	}
	if len(view.NodeIDs) != 1 || view.NodeIDs[0] != node.ID {
		t.Fatalf("route nodes after snapshot/report race = %v, want [%s]", view.NodeIDs, node.ID)
	}
}
