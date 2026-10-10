package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

// adr: 379 — CLI inventory/status/revoke use account control reads without
// requiring or printing laptop credentials; doctor always revokes its probe.
func TestDevBridgeControlCommands(t *testing.T) {
	stdout, restore := captureStdout(t)
	defer restore()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	session, credentials, err := devbridge.NewSession(devbridge.Scope{AccountID: "account", ProjectID: "shop", EnvironmentID: "development", TargetAppID: "payments", DeveloperID: "alice"}, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var revokes atomic.Int32
	var cleanupFails atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.CreateDevBridgeResponse{Session: session, Credentials: credentials})
		case r.Method == "DELETE":
			revokes.Add(1)
			if cleanupFails.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case strings.HasSuffix(r.URL.Path, "/status"):
			if r.Header.Get(devbridge.TokenHeader) != credentials.AttachmentToken || r.Header.Get(devbridge.AccountHeader) != "account" {
				t.Error("doctor did not use scoped attachment authority")
			}
			_, _ = w.Write([]byte(`{"connected":false}`))
		case strings.HasSuffix(r.URL.Path, "/activity"):
			_ = json.NewEncoder(w).Encode(devbridge.Activity{ConnectionState: "connected", Requests: []devbridge.RequestRecord{}})
		case r.URL.Path == "/v1/dev/bridges":
			_ = json.NewEncoder(w).Encode(api.ListDevBridgesResponse{Sessions: []api.DevBridgeSessionSummary{{Session: session, ConnectionState: "connected"}}})
		default:
			_ = json.NewEncoder(w).Encode(session)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-api-token")
	for _, args := range [][]string{{"list"}, {"status", session.ID}, {"revoke", session.ID}} {
		if code := cmdDevBridge(args); code != 0 {
			t.Fatalf("%v returned %d", args, code)
		}
	}
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true
	if code := cmdDevBridge([]string{"status", session.ID}); code != 0 {
		t.Fatalf("JSON status: %d", code)
	}
	jsonOutput = oldJSON
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if code := cmdDevBridge([]string{"doctor", "payments", "--local-port", port}); code != 0 {
		t.Fatalf("doctor returned %d", code)
	}
	if revokes.Load() != 2 {
		t.Fatalf("doctor leaked its session: %d revocations", revokes.Load())
	}
	cleanupFails.Store(true)
	if code := cmdDevBridge([]string{"doctor", "payments", "--local-port", port}); code == 0 {
		t.Fatal("doctor cleanup failure reported success")
	}
	for _, want := range []string{"connected", session.ID, "Bridge revoked", "Relay: reachable", "\"activity\""} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing CLI result %q", want)
		}
	}
	if strings.Contains(stdout.String(), credentials.AttachmentToken) || strings.Contains(stdout.String(), credentials.RequestToken) {
		t.Fatal("authority leaked into CLI output")
	}
}
