package realtime

// adr: 301
// adr: 313

import (
	"context"
	"errors"
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
	endpoints, err := client.Endpoints(context.Background())
	if err != nil || len(endpoints) != 1 || endpoints[0] != "notifications" {
		t.Fatalf("endpoint inventory = (%v, %v), want notifications", endpoints, err)
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
	if err := client.RemoveEndpoint(context.Background(), "notifications"); err != nil {
		t.Fatal(err)
	}
	endpoints, err = client.Endpoints(context.Background())
	if err != nil || len(endpoints) != 0 {
		t.Fatalf("endpoint inventory after removal = (%v, %v), want empty", endpoints, err)
	}
}

func TestClientListsAndReplaysCallbackDeadLetters(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 1})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Fail(event.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	manager := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer manager.Close()
	server := httptest.NewServer(manager.HTTPHandler())
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}

	page, err := client.ListCallbackDeadLetters(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("ListCallbackDeadLetters: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != event.ID || page.NextCursor != "" {
		t.Fatalf("dead-letter page = %+v, want one item for %s", page, event.ID)
	}
	response, err := server.Client().Get(server.URL + "/internal/callbacks/dead-letters")
	if err != nil {
		t.Fatalf("GET dead letters: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("read dead-letter response: %v", readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET dead letters status = %d, want 200", response.StatusCode)
	}
	for _, secret := range []string{"hello", "callback-secret", "https://example.com/callback"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("dead-letter API exposed private callback value %q: %s", secret, body)
		}
	}

	queue.maxBytes = 1
	err = client.ReplayCallbackDeadLetter(context.Background(), event.ID)
	var managementErr *ManagementError
	if !errors.As(err, &managementErr) || managementErr.StatusCode != http.StatusConflict {
		t.Fatalf("ReplayCallbackDeadLetter at capacity = %v, want HTTP 409", err)
	}
	queue.maxBytes = 1 << 20
	if err := client.ReplayCallbackDeadLetter(context.Background(), event.ID); err != nil {
		t.Fatalf("ReplayCallbackDeadLetter: %v", err)
	}
	if err := client.ReplayCallbackDeadLetter(context.Background(), event.ID); err != nil {
		t.Fatalf("repeat ReplayCallbackDeadLetter: %v, want idempotent success", err)
	}
	if stats := queue.Stats(); stats.Pending != 1 || stats.DeadLetterTotal != 0 {
		t.Fatalf("outbox after API replay = %+v, want one pending event", stats)
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
