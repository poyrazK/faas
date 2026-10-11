package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestDevBridgeWebSocketReachesLocalProcessThroughGateway covers the scoped
// upgrade path (ADR-742): gateway handler → relay → laptop tunnel → loopback
// WebSocket server, while unscoped traffic keeps reaching the deployed app
// and revocation closes the live socket.
func TestDevBridgeWebSocketReachesLocalProcessThroughGateway(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "bridge-ws@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "realtime", Type: state.AppTypeApp, Status: state.AppActive, Visibility: api.AppVisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: env.Slug, Status: state.DeployLive, ImageDigest: "sha256:realtime"})
	if err != nil {
		t.Fatal(err)
	}
	backend := &bridgeScenarioBackend{router: pgRouter{store: store, appsSuffix: wire.DeployWildcardSuffix, deploySuffix: wire.DeployWildcardSuffix}, deployments: map[string]state.Deployment{app.ID: dep}}
	router := gateway.NewHandlerWith(backend, gateway.NewMetrics(), discardLogger())
	relay := devbridge.NewRelay(4).WithUpgradeLimits(devbridge.UpgradeLimits{MaxConnections: api.DevBridgeMaxUpgradedConnections, IdleTimeout: api.DevBridgeUpgradeIdleTimeout, MaxBytes: api.DevBridgeUpgradeMaxBytes})
	relayServer := httptest.NewServer(devbridge.NewServer(relay, store.DevBridgeByID, nil))
	defer relayServer.Close()
	relayURL, _ := url.Parse(relayServer.URL)
	router.WithDevBridge(developmentBridgeAuthorization(store), developmentBridgeForwarder(store, relayURL))
	router.WithForwarding(func(gateway.Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "remote-realtime") })
	})
	edge := httptest.NewServer(router)
	defer edge.Close()

	session, creds, err := devbridge.NewSession(devbridge.Scope{AccountID: account.ID, DeveloperID: "alice", ProjectID: project.ID, EnvironmentID: env.ID, TargetAppID: app.ID}, time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDevBridge(ctx, session); err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(devbridge.TokenHeader) != "" {
			t.Error("bridge authority reached the local WebSocket server")
		}
		socket, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		for {
			kind, message, err := socket.ReadMessage()
			if err != nil {
				return
			}
			_ = socket.WriteMessage(kind, append([]byte("local:"), message...))
		}
	}))
	defer local.Close()
	localURL, _ := url.Parse(local.URL)
	laptop, handshake, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(relayServer.URL, "http")+"/v1/dev/bridges/"+session.ID+"/connect", http.Header{devbridge.AccountHeader: []string{account.ID}, devbridge.TokenHeader: []string{creds.AttachmentToken}})
	if handshake != nil {
		_ = handshake.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer laptop.Close()
	go func() {
		_ = devbridge.ServeLocal(ctx, devbridge.NewWebSocketConn(laptop), localURL, api.DevBridgeMaxConcurrentRequests+api.DevBridgeMaxUpgradedConnections)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !relay.Connected(session.ID) {
		if time.Now().After(deadline) {
			t.Fatal("laptop did not attach")
		}
		time.Sleep(time.Millisecond)
	}
	host := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, env.ID, app.ID)
	socket, response, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(edge.URL, "http")+"/live", http.Header{
		"Host":                  []string{host},
		devbridge.SessionHeader: []string{session.ID},
		devbridge.AccountHeader: []string{account.ID},
		devbridge.TokenHeader:   []string{creds.RequestToken},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("scoped WebSocket dial: %v (status %d)", err, status)
	}
	defer socket.Close()
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := socket.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, message, err := socket.ReadMessage(); err != nil || string(message) != "local:ping" {
		t.Fatalf("scoped WebSocket echo = %q, %v", message, err)
	}

	request, _ := http.NewRequestWithContext(ctx, "GET", edge.URL+"/", nil)
	request.Host = host
	ordinary, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(ordinary.Body)
	_ = ordinary.Body.Close()
	if string(body) != "remote-realtime" {
		t.Fatalf("ordinary traffic changed: %d %s", ordinary.StatusCode, body)
	}

	if err := store.RevokeDevBridge(ctx, account.ID, session.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := socket.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("revoked session kept its WebSocket open")
			}
			break
		}
	}
}
