package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
)

// An endpoint at its max_connections refuses the next handshake with a 429
// problem naming the limit and the observed count, like the account limits,
// rather than a plain-text body.
func TestEndpointConnectionLimitAnswersProblem(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "ep", MaxConnections: 1}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "ep"
	first, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	deadline := time.Now().Add(time.Second)
	for len(m.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	_, response, err = websocket.DefaultDialer.Dial(url, nil)
	if err == nil || response == nil {
		t.Fatalf("second dial err = %v, want a refused handshake", err)
	}
	defer response.Body.Close()
	var problem api.Problem
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status = %d, content type %q; want 429 problem+json", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != api.CodeRealtimeConnectionLimit || problem.Limit == nil || *problem.Limit != 1 || problem.Observed == nil || *problem.Observed != 1 {
		t.Fatalf("problem = %+v, want realtime connection limit 1 observed 1", problem)
	}
}
