package receivers

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestRealtimeUsageRecordedEndToEnd runs the ADR-1323 path with real parts:
// a realtime Manager's connections, its usage recorder, the apidgrpc client,
// apid's fleet receiver and PgStore.
func TestRealtimeUsageRecordedEndToEnd(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "realtime-usage@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "realtime-usage", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		AccountID: account.ID, AppID: app.ID, Enabled: true, CallbackURL: "https://app.example",
		ConnectPath: "/realtime/connect", MessagePath: "/realtime/message", DisconnectPath: "/realtime/disconnect",
		CallbackAuthTokenSealed: []byte("sealed"), AuthTokenSealed: []byte("sealed"), AuthMode: api.RealtimeAuthModeStaticBearer,
	}, 10, 50)
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
	client := apidgrpc.NewRealtimeHistoryClientForNode(conn, state.DefaultLocalNodeName)

	manager := realtime.NewManager(realtime.Config{UsageReporter: client, UsageSampleInterval: 10 * time.Millisecond, UsageReportInterval: 50 * time.Millisecond}, realtime.NopHooks{})
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.RegisterEndpoint(realtime.Endpoint{Preview: true, ID: endpoint.ID, AppID: app.ID, AccountID: account.ID,
		Authorize: func(context.Context, *http.Request) (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(manager.Handler())
	t.Cleanup(httpServer.Close)
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + realtime.ManagedPathPrefix + endpoint.ID
	for range 2 {
		ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Close() })
	}

	hour := time.Now().UTC().Truncate(time.Hour)
	deadline := time.Now().Add(10 * time.Second)
	for {
		usage, err := store.ReadRealtimeUsage(ctx, account.ID, hour.Add(-time.Hour), hour.Add(time.Hour))
		if err != nil {
			t.Fatalf("ReadRealtimeUsage: %v", err)
		}
		if len(usage) > 0 && usage[len(usage)-1].PeakConnections == 2 {
			got := usage[len(usage)-1]
			if got.EndpointID != endpoint.ID || got.AppID != app.ID {
				t.Fatalf("usage attributed to %s/%s, want %s/%s", got.EndpointID, got.AppID, endpoint.ID, app.ID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage never recorded a peak of 2 connections: %+v (stats %+v)", usage, manager.Stats())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
