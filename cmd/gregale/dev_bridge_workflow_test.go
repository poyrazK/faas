//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

func TestDevBridgeCommandHelper(t *testing.T) {
	if os.Getenv("GREGALE_BRIDGE_HELPER") != "1" {
		return
	}
	if os.Getenv("FAAS_TOKEN") != "" || os.Getenv("INVENTORY_URL") == "" || os.Getenv("INVENTORY_URL") != os.Getenv("GREGALE_SERVICE_INVENTORY_URL") {
		os.Exit(9)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(os.Getenv("HOST"), os.Getenv("PORT")))
	if err != nil {
		os.Exit(9)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/exit" {
			w.WriteHeader(204)
			go func() { time.Sleep(50 * time.Millisecond); os.Exit(7) }()
			return
		}
		response, err := http.Get(os.Getenv("INVENTORY_URL") + "/stock") //nolint:noctx // Short-lived subprocess fixture.
		if err != nil {
			http.Error(w, "dependency failed", 502)
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(w, response.Body)
	})}
	_ = server.Serve(listener)
	os.Exit(0)
}

// adr: 379 — use an actual supervised child, relay and loopback dependency
// proxy; preserve its exit code and revoke durable intent when it stops.
func TestDevBridgeCommandToLocalProcessToRemoteDependency(t *testing.T) {
	stdout, restore := captureStdout(t)
	defer restore()
	t.Setenv("GREGALE_BRIDGE_HELPER", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	_ = reservation.Close()
	var mu sync.Mutex
	var created api.CreateDevBridgeResponse
	revoked := false
	relay := devbridge.NewRelay(api.DevBridgeMaxConcurrentRequests)
	lookup := func(_ context.Context, account, id string) (devbridge.Session, error) {
		mu.Lock()
		defer mu.Unlock()
		if account != "account" || id != created.Session.ID {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		return created.Session, nil
	}
	remoteDependency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(devbridge.TokenHeader) != "" {
			t.Error("attachment credential leaked to dependency")
		}
		_, _ = io.WriteString(w, "remote inventory")
	}))
	defer remoteDependency.Close()
	dependencyURL, _ := url.Parse(remoteDependency.URL)
	dependencyProxy := httputil.NewSingleHostReverseProxy(dependencyURL)
	internalGateway := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		session := created.Session
		mu.Unlock()
		if err := session.AuthorizeDependency(time.Now(), r.Header.Get(devbridge.TokenHeader), r.Header.Get(devbridge.AccountHeader), session.Scope.EnvironmentID, "inventory"); err != nil {
			http.Error(w, "dependency denied", 403)
			return
		}
		devbridge.ClearCredentials(r.Header)
		dependencyProxy.ServeHTTP(w, r)
	})
	relayHandler := devbridge.NewServer(relay, lookup, internalGateway)
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/dev/bridges":
			session, credentials, err := devbridge.NewSession(devbridge.Scope{AccountID: "account", DeveloperID: "alice", ProjectID: "shop", EnvironmentID: "development", TargetAppID: "payments", DependencyAppIDs: []string{"inventory"}}, time.Now(), time.Now().Add(time.Hour))
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			mu.Lock()
			created = api.CreateDevBridgeResponse{Session: session, Credentials: credentials, EnvironmentURL: base, Dependencies: []api.DevBridgeDependency{{AppID: "inventory", Name: "inventory"}}}
			out := created
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == "DELETE":
			mu.Lock()
			now := time.Now()
			created.Session.RevokedAt, revoked = &now, true
			mu.Unlock()
			w.WriteHeader(204)
		default:
			relayHandler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	t.Setenv("FAAS_API", base)
	t.Setenv("FAAS_TOKEN", "test-token")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- cmdDevBridge([]string{"payments", "--local-port", fmt.Sprint(port), "--ready-path", "/health", "--bind-env", "INVENTORY_URL=inventory", "--", executable, "-test.run=^TestDevBridgeCommandHelper$"})
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for !strings.Contains(stdout.String(), "Bridge connected.") {
		select {
		case code := <-done:
			t.Fatalf("bridge exited during startup: %d output=%s", code, stdout.String())
		case <-ctx.Done():
			t.Fatal("bridge did not attach")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	session := created
	mu.Unlock()
	// The client reports the WebSocket upgrade before the relay finishes
	// registering its HTTP/2 connection. Wait for that server-side readiness.
	for !relay.Connected(session.Session.ID) {
		select {
		case code := <-done:
			t.Fatalf("bridge exited before relay readiness: %d output=%s", code, stdout.String())
		case <-ctx.Done():
			t.Fatal("relay did not register the bridge")
		case <-time.After(10 * time.Millisecond):
		}
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", base+"/v1/dev/bridges/"+session.Session.ID+"/traffic/charge", nil)
	request.Header.Set(devbridge.AccountHeader, "account")
	request.Header.Set(devbridge.TokenHeader, session.Credentials.RequestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || string(body) != "remote inventory" {
		t.Fatalf("local-to-remote: %d %s", response.StatusCode, body)
	}
	request.URL.Path = "/v1/dev/bridges/" + session.Session.ID + "/traffic/exit"
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	select {
	case code := <-done:
		if code != 7 {
			t.Fatalf("child exit code lost: %d", code)
		}
	case <-ctx.Done():
		t.Fatal("bridge did not stop after child exit")
	}
	mu.Lock()
	wasRevoked := revoked
	mu.Unlock()
	if !wasRevoked {
		t.Fatal("session not revoked after child exit")
	}
	if strings.Contains(stdout.String(), session.Credentials.AttachmentToken) || strings.Contains(stdout.String(), session.Credentials.RequestToken) {
		t.Fatal("credential leaked to output")
	}
}
