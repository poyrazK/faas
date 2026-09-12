package realtime

import (
	"context"
	"net/http/httptest"
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
