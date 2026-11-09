package realtime

import (
	"errors"
	"net/http"
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
	if !errors.As(err, &closed) || closed.Code != websocket.ClosePolicyViolation || closed.Text != "endpoint removed or disabled" {
		t.Fatalf("client saw %v, want close 1008 \"endpoint removed or disabled\"", err)
	}
}

// A settings change that no longer admits a socket (here a finalized token
// rotation) closes it with 1008 and a reason, as the docs promise, not a bare
// TCP close the client sees as 1006 and retries like a network drop.
func TestEndpointUpdateClosesRefusedSocketWithPolicyViolation(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	endpoint := Endpoint{Preview: true, ID: "ep", ClientAuth: AuthPolicy{Mode: AuthModeStaticBearer}, AuthToken: "old"}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	header := http.Header{"Authorization": {"Bearer old"}}
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+"ep", header)
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
	endpoint.AuthToken = "new"
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = client.ReadMessage()
	var closed *websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.ClosePolicyViolation || closed.Text != "endpoint credentials or origins changed" {
		t.Fatalf("client saw %v, want close 1008 \"endpoint credentials or origins changed\"", err)
	}
}
