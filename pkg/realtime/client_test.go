package realtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientManagementAPI(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	server := httptest.NewServer(m.HTTPHandler())
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if err := client.RegisterEndpoint(context.Background(), Endpoint{ID: "notifications"}); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	if err := client.Subscribe(context.Background(), "missing", "alerts"); err == nil {
		t.Fatal("subscribe missing connection unexpectedly succeeded")
	}
	stats, err := client.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.CurrentConnections != 0 {
		t.Fatalf("current connections = %d, want 0", stats.CurrentConnections)
	}
	connections, err := client.Connections(context.Background())
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(connections) != 0 {
		t.Fatalf("connections = %d, want 0", len(connections))
	}
}

func TestClientManagementResponseIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", managementResponseMaxBytes+1))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := client.Stats(context.Background())
	if err == nil {
		t.Fatal("Stats unexpectedly accepted an oversized management response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Stats error = %v, want bounded-response error", err)
	}
}
