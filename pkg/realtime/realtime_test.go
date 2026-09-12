package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testHooks struct {
	mu         sync.Mutex
	connect    []Event
	messages   []Event
	disconnect []Event
	accept     bool
}

func (h *testHooks) Connect(_ context.Context, event Event) (bool, error) {
	h.mu.Lock()
	h.connect = append(h.connect, event)
	h.mu.Unlock()
	return h.accept, nil
}

func (h *testHooks) Message(_ context.Context, event Event) error {
	h.mu.Lock()
	h.messages = append(h.messages, event)
	h.mu.Unlock()
	return nil
}

func (h *testHooks) Disconnect(_ context.Context, event Event) error {
	h.mu.Lock()
	h.disconnect = append(h.disconnect, event)
	h.mu.Unlock()
	return nil
}

func TestManagerConnectionLifecycleAndPublish(t *testing.T) {
	hooks := &testHooks{accept: true}
	m := NewManager(Config{
		Heartbeat:        20 * time.Millisecond,
		PongWait:         time.Second,
		MaxConnectionAge: time.Second,
		OutboundQueue:    2,
	}, hooks)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{
		ID:        "notifications",
		AppID:     "app-1",
		AccountID: "acct-1",
		Authorize: func(context.Context, *http.Request) (string, error) { return "", nil },
	}); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}

	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "notifications"
	client, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	deadline := time.Now().Add(time.Second)
	for len(m.Snapshot()) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	connections := m.Snapshot()
	if len(connections) != 1 {
		t.Fatalf("connections = %d, want 1", len(connections))
	}
	connectionID := connections[0].ID
	if err := m.Subscribe(connectionID, "alerts"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := m.Snapshot()[0].Channels; len(got) != 1 || got[0] != "alerts" {
		t.Fatalf("channels = %v, want [alerts]", got)
	}
	if err := client.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		hooks.mu.Lock()
		messages := append([]Event(nil), hooks.messages...)
		hooks.mu.Unlock()
		if len(messages) == 1 {
			if messages[0].ConnectionID != connectionID || string(messages[0].Data) != "hello" || messages[0].Sequence != 1 {
				t.Fatalf("message event = %+v", messages[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message callback not observed")
		}
		time.Sleep(time.Millisecond)
	}

	if queued, err := m.Publish(context.Background(), "notifications", "alerts", Message{Data: []byte("published")}); err != nil || queued != 1 {
		t.Fatalf("publish = (%d, %v), want (1, nil)", queued, err)
	}
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read published message: %v", err)
	}
	if string(data) != "published" {
		t.Fatalf("published data = %q", data)
	}

	if err := m.Send(context.Background(), connectionID, Message{Data: []byte("reply")}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, data, err = client.ReadMessage()
	if err != nil {
		t.Fatalf("read send message: %v", err)
	}
	if string(data) != "reply" {
		t.Fatalf("send data = %q", data)
	}
	stats := m.Stats()
	if stats.CurrentConnections != 1 || stats.AcceptedConnections != 1 || stats.ReceivedMessages != 1 || stats.ReceivedBytes != uint64(len("hello")) || stats.SentMessages != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestManagerRejectsUnauthorizedAndCapsConnections(t *testing.T) {
	hooks := &testHooks{accept: true}
	m := NewManager(Config{MaxConnections: 1}, hooks)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{
		ID: "private",
		Authorize: func(context.Context, *http.Request) (string, error) {
			return "", ErrUnauthorized
		},
	}); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "private"
	_, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != 401 {
		t.Fatalf("unauthorized dial = (%v, %v), want HTTP 401", err, response)
	}

	m.RemoveEndpoint("private")
	if err := m.RegisterEndpoint(Endpoint{ID: "public"}); err != nil {
		t.Fatalf("register public: %v", err)
	}
	url = "ws" + server.URL[len("http"):] + ManagedPathPrefix + "public"
	first, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer first.Close()
	_, response, err = websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != 429 {
		t.Fatalf("capped dial = (%v, %v), want HTTP 429", err, response)
	}
}

func TestManagerAllowsIdleConnectionUntilFirstHeartbeat(t *testing.T) {
	m := NewManager(Config{Heartbeat: 200 * time.Millisecond, PongWait: 20 * time.Millisecond}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{ID: "idle"}); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "idle"
	client, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	time.Sleep(50 * time.Millisecond)
	if got := len(m.Snapshot()); got != 1 {
		t.Fatalf("idle connection count = %d, want 1 before first heartbeat", got)
	}
}

func TestManagerOperationsReturnStableErrors(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{ID: "bad", MessagePath: "/events?admin=true"}); err == nil {
		t.Fatal("endpoint with callback query unexpectedly registered")
	}
	if err := m.Send(context.Background(), "missing", Message{}); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("send missing = %v", err)
	}
	if err := m.Subscribe("missing", "bad\nchannel"); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("subscribe invalid = %v", err)
	}
	if _, err := m.Publish(context.Background(), "missing", "", Message{}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("publish invalid = %v", err)
	}
	if _, err := m.Publish(context.Background(), "missing", "alerts", Message{}); !errors.Is(err, ErrEndpointNotFound) {
		t.Fatalf("publish unknown endpoint = %v", err)
	}
}
