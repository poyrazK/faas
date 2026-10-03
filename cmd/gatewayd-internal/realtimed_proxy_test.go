package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

func TestRealtimedProxyForwardsBrowserResumeHandshake(t *testing.T) {
	socket := "/tmp/rt-" + uuid.NewString() + ".sock"
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	seenProtocols := make(chan []string, 1)
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenProtocols <- websocket.Subprotocols(r)
		upgrader := websocket.Upgrader{
			Subprotocols: []string{realtime.ResumeSubprotocol},
			CheckOrigin:  func(*http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	})}
	go func() { _ = backend.Serve(listener) }()
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	front := httptest.NewServer(newRealtimedProxy(socket, nil))
	defer front.Close()
	url := "ws" + strings.TrimPrefix(front.URL, "http") + realtime.ManagedPathPrefix + "endpoint"
	credential := realtime.ResumeBearerSubprotocolPrefix + "aaa.bbb.ccc"
	dialer := websocket.Dialer{Subprotocols: []string{realtime.ResumeSubprotocol, credential}}
	conn, response, err := dialer.Dial(url, http.Header{"Origin": {"https://app.example"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.Subprotocol(); got != realtime.ResumeSubprotocol {
		t.Fatalf("selected protocol = %q", got)
	}
	got := <-seenProtocols
	if len(got) != 2 || got[0] != realtime.ResumeSubprotocol || got[1] != credential {
		t.Fatalf("proxied protocols = %v", got)
	}
}

func TestRealtimedControlProxyRewritesManagementPath(t *testing.T) {
	// macOS limits Unix socket paths to a small fixed size; t.TempDir()
	// paths can exceed it under the repository's long test names.
	socket := "/tmp/rt-" + uuid.NewString() + ".sock"
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	manager := realtime.NewManager(realtime.Config{}, nil)
	server := &http.Server{Handler: manager.HTTPHandler()}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = manager.Close()
		_ = server.Shutdown(context.Background())
	})

	proxy := newRealtimedControlProxy(socket, nil)
	if proxy == nil {
		t.Fatal("proxy is nil")
	}
	req := httptest.NewRequest(http.MethodGet, "http://node/v1/internal/realtime/internal/stats", nil)
	resp := httptest.NewRecorder()
	proxy.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", resp.Code, resp.Body.String())
	}
}

func TestRealtimedControlProxyDisabledWithoutSocket(t *testing.T) {
	if got := newRealtimedControlProxy("", nil); got != nil {
		t.Fatal("empty socket should disable control proxy")
	}
}

func TestRealtimedProxyFailureReturnsSafeProblem(t *testing.T) {
	missingSocket := filepath.Join(t.TempDir(), "missing-realtimed.sock")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name string
		new  func(string, *slog.Logger) http.Handler
		path string
	}{
		{name: "public", new: newRealtimedProxy, path: "/realtime/connect"},
		{name: "control", new: newRealtimedControlProxy, path: "/v1/internal/realtime/connections"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			test.new(missingSocket, log).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
				t.Fatalf("content-type = %q, want application/problem+json", got)
			}
			if got := recorder.Header().Get("Retry-After"); got != "5" {
				t.Errorf("Retry-After = %q, want 5", got)
			}
			var problem api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != api.CodeRealtimeUnavailable {
				t.Errorf("code = %q, want %q", problem.Code, api.CodeRealtimeUnavailable)
			}
			if strings.Contains(recorder.Body.String(), missingSocket) || strings.Contains(recorder.Body.String(), "dial unix") {
				t.Errorf("response leaked internal dial details: %s", recorder.Body.String())
			}
		})
	}
}
