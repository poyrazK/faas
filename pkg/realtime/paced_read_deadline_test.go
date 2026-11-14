package realtime

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
)

// A client that sends faster than the message rate is read at that rate;
// while it waits its pongs sit behind its own frames, so the frames
// themselves must keep the read deadline alive. Before, the socket was
// dropped after Heartbeat+PongWait and the rest of its frames were lost.
func TestPacedClientMessagesOutliveReadDeadline(t *testing.T) {
	hooks := &testHooks{accept: true}
	m := NewManager(Config{Heartbeat: 100 * time.Millisecond, PongWait: 100 * time.Millisecond}, hooks)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "ep"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+"ep", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// The burst is admitted at once; the rest wait for the rate, together
	// longer than the 200 ms read deadline.
	total := api.RealtimeClientMessageBurst + 10
	for i := range total {
		if err := client.WriteMessage(websocket.TextMessage, []byte{byte('a' + i%26)}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		hooks.mu.Lock()
		got := len(hooks.messages)
		hooks.mu.Unlock()
		if got == total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message callbacks = %d, want %d (connections open: %d)", got, total, len(m.Snapshot()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.Snapshot()) != 1 {
		t.Fatal("connection closed while its paced frames were read")
	}
}
