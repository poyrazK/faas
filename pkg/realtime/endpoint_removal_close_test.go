package realtime

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Removing an endpoint tells each client why, so it does not reconnect as it
// would after a network drop.
func TestRemoveEndpointSendsEndpointRemovedClose(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "gone"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+"gone", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(time.Second)
	for len(m.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	m.RemoveEndpoint("gone")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RemoveEndpoint took %s", elapsed)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = client.ReadMessage()
	var closed *websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.ClosePolicyViolation || closed.Text != "endpoint removed" {
		t.Fatalf("client saw %v, want close 1008 \"endpoint removed\"", err)
	}
}
