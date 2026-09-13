package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/realtime"
)

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
