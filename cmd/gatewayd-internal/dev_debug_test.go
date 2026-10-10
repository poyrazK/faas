package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIsDevDebugPath(t *testing.T) {
	for path, want := range map[string]string{
		"/v1/apps/dev-api-0123/debug":   "dev-api-0123",
		"/v1/apps/dev-api-0123/debug/x": "",
		"/v1/apps/dev-api-0123/logs":    "",
		"/v1/apps//debug":               "",
		"/v1/apps/dev-api-0123":         "",
	} {
		slug, ok := isDevDebugPath(path)
		if (want != "") != ok || slug != want && ok {
			t.Fatalf("isDevDebugPath(%q) = %q, %t; want %q", path, slug, ok, want)
		}
	}
}

func TestDevDebugSessionsBoundPerApp(t *testing.T) {
	var sessions devDebugSessions
	first, ok := sessions.acquire("app", 1)
	if !ok {
		t.Fatal("first session refused")
	}
	if _, ok := sessions.acquire("app", 1); ok {
		t.Fatal("second session admitted over the limit")
	}
	if _, ok := sessions.acquire("other", 1); !ok {
		t.Fatal("limit leaked across apps")
	}
	first()
	first() // idempotent
	if _, ok := sessions.acquire("app", 1); !ok {
		t.Fatal("released slot was not reusable")
	}
}

type fakeDebugTargets struct{ woke []string }

func (f *fakeDebugTargets) WakeTarget(_ context.Context, appID string, port int) (gateway.Target, error) {
	f.woke = append(f.woke, appID)
	return gateway.Target{AppID: appID, NodeID: "node-1", InstanceID: "instance-1", Port: port}, nil
}

type devDebugFixture struct {
	server  *httptest.Server
	key     string
	devApp  state.App
	prodApp state.App
	targets *fakeDebugTargets
	mu      sync.Mutex
	ports   []int
}

func newDevDebugFixture(t *testing.T) *devDebugFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "debug@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	devApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "dev-api-0123456789ab", Type: state.AppTypeApp, RAMMB: 256,
		MaxConcurrency: 1, IdleTimeoutS: 60, PreviewOfSlug: "api", PreviewPrState: state.PreviewPrStateOpen})
	if err != nil {
		t.Fatal(err)
	}
	prodApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	f := &devDebugFixture{key: key, devApp: devApp, prodApp: prodApp, targets: &fakeDebugTargets{}}
	auth := mwauth.New(store, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	tunnel := &devDebugTunnel{auth: auth, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		forward: func(_ context.Context, conn net.Conn, target gateway.Target) error {
			f.mu.Lock()
			f.ports = append(f.ports, target.Port)
			f.mu.Unlock()
			buf := make([]byte, 64)
			n, err := conn.Read(buf)
			if err != nil {
				return err
			}
			_, err = conn.Write(append([]byte("inspector:"), buf[:n]...))
			return err
		}}
	tunnel.setTargets(f.targets)
	f.server = httptest.NewServer(devDebugRoute(tunnel, http.NotFoundHandler()))
	t.Cleanup(f.server.Close)
	return f
}

func (f *devDebugFixture) dial(slug string, header http.Header) (*websocket.Conn, int, error) {
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/v1/apps/" + slug + "/debug"
	socket, response, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).Dial(url, header)
	status := 0
	if response != nil {
		status = response.StatusCode
		_ = response.Body.Close()
	}
	return socket, status, err
}

func TestDevDebugTunnelReachesInspectorPortOfDeveloperApp(t *testing.T) {
	f := newDevDebugFixture(t)
	socket, status, err := f.dial(f.devApp.Slug, http.Header{"Authorization": {"Bearer " + f.key}})
	if err != nil {
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	defer func() { _ = socket.Close() }()
	if err := socket.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, body, err := socket.ReadMessage()
	if err != nil || string(body) != "inspector:hello" {
		t.Fatalf("echo = %q, %v", body, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ports) != 1 || f.ports[0] != api.DevDebugNodePort || len(f.targets.woke) != 1 || f.targets.woke[0] != f.devApp.ID {
		t.Fatalf("forwarded ports %v, woke %v; want the developer app's inspector port", f.ports, f.targets.woke)
	}
}

func TestDevDebugTunnelRefusals(t *testing.T) {
	cases := []struct {
		name   string
		slug   func(*devDebugFixture) string
		header func(*devDebugFixture) http.Header
		want   int
	}{
		{name: "no credentials", slug: func(f *devDebugFixture) string { return f.devApp.Slug },
			header: func(*devDebugFixture) http.Header { return http.Header{} }, want: http.StatusUnauthorized},
		{name: "production app", slug: func(f *devDebugFixture) string { return f.prodApp.Slug },
			header: func(f *devDebugFixture) http.Header { return http.Header{"Authorization": {"Bearer " + f.key}} }, want: http.StatusConflict},
		{name: "unknown app", slug: func(*devDebugFixture) string { return "nope-0000" },
			header: func(f *devDebugFixture) http.Header { return http.Header{"Authorization": {"Bearer " + f.key}} }, want: http.StatusNotFound},
		{name: "browser origin", slug: func(f *devDebugFixture) string { return f.devApp.Slug },
			header: func(f *devDebugFixture) http.Header {
				return http.Header{"Authorization": {"Bearer " + f.key}, "Origin": {"https://evil.example"}}
			}, want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDevDebugFixture(t)
			if _, status, err := f.dial(tc.slug(f), tc.header(f)); err == nil || status != tc.want {
				t.Fatalf("dial = %v (status %d), want status %d", err, status, tc.want)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.ports) != 0 {
				t.Fatal("a refused request reached the inspector")
			}
		})
	}
}

func TestDevDebugTunnelRequiresWebSocket(t *testing.T) {
	f := newDevDebugFixture(t)
	request, _ := http.NewRequest(http.MethodGet, f.server.URL+"/v1/apps/"+f.devApp.Slug+"/debug", nil)
	request.Header.Set("Authorization", "Bearer "+f.key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain GET = %d, want 400", response.StatusCode)
	}
}
