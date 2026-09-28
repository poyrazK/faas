package realtime

// adr: 317

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestManagerClosesSocketWhenCallbackOutboxStaysFull(t *testing.T) {
	var messageRequests atomic.Uint64
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/message" {
			messageRequests.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxBytes: 1, RetryInterval: time.Millisecond})
	manager := NewManager(Config{CallbackTimeout: 80 * time.Millisecond}, HTTPHooks{
		Client:       callbackServer.Client(),
		DurableQueue: queue,
		MaxAttempts:  1,
	})
	defer manager.Close()
	endpoint := Endpoint{
		ID:             "full-outbox",
		CallbackURL:    callbackServer.URL,
		ConnectPath:    "/connect",
		MessagePath:    "/message",
		DisconnectPath: "/disconnect",
	}
	if err := manager.RegisterEndpoint(endpoint); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+endpoint.ID, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(websocket.TextMessage, []byte("first")); err != nil {
		t.Fatalf("write first message: %v", err)
	}
	if err := client.WriteMessage(websocket.TextMessage, []byte("second")); err != nil {
		t.Fatalf("write second message: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = client.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("connection close = %v, want websocket code %d", err, websocket.CloseTryAgainLater)
	}

	deadline := time.Now().Add(time.Second)
	for len(manager.Snapshot()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(manager.Snapshot()) != 0 {
		t.Fatal("connection remained registered after outbox-full close")
	}
	stats := manager.Stats()
	if stats.ReceivedMessages != 1 {
		t.Fatalf("received messages = %d, want only the first message before closing", stats.ReceivedMessages)
	}
	if stats.CallbackOutboxFull == 0 {
		t.Fatal("outbox-full rejection was not counted")
	}
	if got := messageRequests.Load(); got != 0 {
		t.Fatalf("message callbacks reached receiver = %d, want 0", got)
	}
	if got := queue.Stats().Pending; got != 0 {
		t.Fatalf("pending callbacks = %d, want no unpersisted message", got)
	}
}

func TestManagerClosesSocketWhenCallbackOutboxPersistenceFails(t *testing.T) {
	var messageRequests atomic.Uint64
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/message" {
			messageRequests.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()

	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	queue.root += "-unavailable"
	manager := NewManager(Config{CallbackTimeout: time.Second}, HTTPHooks{
		Client:       callbackServer.Client(),
		DurableQueue: queue,
		MaxAttempts:  1,
	})
	defer manager.Close()
	endpoint := Endpoint{
		ID:             "outbox-write-failure",
		CallbackURL:    callbackServer.URL,
		ConnectPath:    "/connect",
		MessagePath:    "/message",
		DisconnectPath: "/disconnect",
	}
	if err := manager.RegisterEndpoint(endpoint); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+endpoint.ID, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(websocket.TextMessage, []byte("first")); err != nil {
		t.Fatalf("write first message: %v", err)
	}
	if err := client.WriteMessage(websocket.TextMessage, []byte("second")); err != nil {
		t.Fatalf("write second message: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = client.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("connection close = %v, want websocket code %d", err, websocket.CloseTryAgainLater)
	}

	deadline := time.Now().Add(time.Second)
	for len(manager.Snapshot()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(manager.Snapshot()) != 0 {
		t.Fatal("connection remained registered after outbox persistence failure")
	}
	stats := manager.Stats()
	if stats.ReceivedMessages != 1 {
		t.Fatalf("received messages = %d, want only the first message before closing", stats.ReceivedMessages)
	}
	if stats.CallbackOutboxAdmissionErrors == 0 {
		t.Fatal("outbox admission failure was not counted")
	}
	if stats.CallbackOutboxFull != 0 {
		t.Fatalf("outbox-full count = %d, want persistence failure classified separately", stats.CallbackOutboxFull)
	}
	if got := messageRequests.Load(); got != 0 {
		t.Fatalf("message callbacks reached receiver = %d, want 0", got)
	}
	if got := queue.Stats().Pending; got != 0 {
		t.Fatalf("pending callbacks = %d, want no unpersisted message", got)
	}
}

func TestManagerClosesSocketWhenUnpersistedCallbackFails(t *testing.T) {
	var messageRequests atomic.Uint64
	var disconnectRequests atomic.Uint64
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/connect":
			w.WriteHeader(http.StatusNoContent)
		case "/disconnect":
			disconnectRequests.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/message":
			messageRequests.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer callbackServer.Close()

	manager := NewManager(Config{CallbackTimeout: time.Second}, HTTPHooks{
		Client:      callbackServer.Client(),
		MaxAttempts: 1,
	})
	defer manager.Close()
	endpoint := Endpoint{
		ID:             "unpersisted-callback",
		CallbackURL:    callbackServer.URL,
		ConnectPath:    "/connect",
		MessagePath:    "/message",
		DisconnectPath: "/disconnect",
	}
	if err := manager.RegisterEndpoint(endpoint); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+endpoint.ID, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(websocket.TextMessage, []byte("first")); err != nil {
		t.Fatalf("write first message: %v", err)
	}
	if err := client.WriteMessage(websocket.TextMessage, []byte("second")); err != nil {
		t.Fatalf("write second message: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = client.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("connection close = %v, want websocket code %d", err, websocket.CloseTryAgainLater)
	}

	deadline := time.Now().Add(time.Second)
	for (len(manager.Snapshot()) != 0 || manager.Stats().CallbackUnpersistedFailures < 2) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(manager.Snapshot()) != 0 {
		t.Fatal("connection remained registered after non-durable callback failure")
	}
	stats := manager.Stats()
	if stats.ReceivedMessages != 1 {
		t.Fatalf("received messages = %d, want only the first message before closing", stats.ReceivedMessages)
	}
	if stats.CallbackUnpersistedFailures != 2 {
		t.Fatalf("unpersisted callback failures = %d, want message and disconnect failures", stats.CallbackUnpersistedFailures)
	}
	if got := messageRequests.Load(); got != 1 {
		t.Fatalf("message callback requests = %d, want only the failed first message", got)
	}
	if got := disconnectRequests.Load(); got != 1 {
		t.Fatalf("disconnect callback requests = %d, want one disconnect attempt", got)
	}
}

func TestManagerUpdatesCallbackAuthForExistingConnections(t *testing.T) {
	hooks := &testHooks{accept: true}
	m := NewManager(Config{MaxConnectionAge: time.Second}, hooks)
	defer m.Close()
	endpoint := Endpoint{ID: "rotating-callback", CallbackAuthToken: "callback-old"}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+endpoint.ID, nil)
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
	if len(m.Snapshot()) != 1 {
		t.Fatal("connection was not registered")
	}

	hooks.mu.Lock()
	if len(hooks.connect) != 1 || hooks.connect[0].CallbackAuthToken != "callback-old" {
		hooks.mu.Unlock()
		t.Fatalf("connect callback = %+v, want original token", hooks.connect)
	}
	hooks.mu.Unlock()

	endpoint.CallbackAuthToken = "callback-new"
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatalf("rotate callback token: %v", err)
	}
	if err := client.WriteMessage(websocket.TextMessage, []byte("after-rotation")); err != nil {
		t.Fatalf("write message: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		messages := append([]Event(nil), hooks.messages...)
		hooks.mu.Unlock()
		if len(messages) == 1 {
			if messages[0].CallbackAuthToken != "callback-new" {
				t.Fatalf("message callback token = %q, want updated token", messages[0].CallbackAuthToken)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	hooks.mu.Lock()
	if len(hooks.messages) != 1 {
		hooks.mu.Unlock()
		t.Fatal("message callback was not observed")
	}
	hooks.mu.Unlock()

	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		disconnects := append([]Event(nil), hooks.disconnect...)
		hooks.mu.Unlock()
		if len(disconnects) == 1 {
			if disconnects[0].CallbackAuthToken != "callback-new" {
				t.Fatalf("disconnect callback token = %q, want updated token", disconnects[0].CallbackAuthToken)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("disconnect callback was not observed")
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

func TestManagerKeepsIdleConnectionAcrossPongs(t *testing.T) {
	m := NewManager(Config{Heartbeat: 200 * time.Millisecond, PongWait: 150 * time.Millisecond, MaxConnectionAge: 3 * time.Second}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{ID: "idle"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	client, response, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+ManagedPathPrefix+"idle", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var pings atomic.Int32
	client.SetPingHandler(func(data string) error {
		pings.Add(1)
		return client.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	done := make(chan error, 1)
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				done <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for pings.Load() < 2 && time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("client closed before second heartbeat: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if pings.Load() < 2 || len(m.Snapshot()) != 1 {
		t.Fatalf("idle connection did not survive two heartbeats: pings=%d connections=%d", pings.Load(), len(m.Snapshot()))
	}
}

func TestManagerReregisterPreservesEndpointConnectionLimit(t *testing.T) {
	m := NewManager(Config{MaxConnections: 10, Heartbeat: time.Hour}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{ID: "limited", MaxConnections: 1}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "limited"
	first, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := m.RegisterEndpoint(Endpoint{ID: "limited", MaxConnections: 1}); err != nil {
		t.Fatal(err)
	}
	second, response, err := websocket.DefaultDialer.Dial(url, nil)
	if second != nil {
		_ = second.Close()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second connection = (%v, %v), want 429", err, response)
	}
}

func TestManagerRemovalClosesExistingConnections(t *testing.T) {
	m := NewManager(Config{MaxMessageBytes: 16, Heartbeat: time.Hour}, nil)
	defer m.Close()
	if err := m.RegisterEndpoint(Endpoint{ID: "limited", MaxMessageBytes: 4}); err != nil {
		t.Fatalf("register endpoint: %v", err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + ManagedPathPrefix + "limited"
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
	m.RemoveEndpoint("limited")
	if _, err := m.Publish(context.Background(), "limited", "alerts", Message{Data: []byte("ok")}); !errors.Is(err, ErrEndpointNotFound) {
		t.Fatalf("publish after removal = %v, want endpoint not found", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = client.ReadMessage()
	if err == nil {
		t.Fatal("removed endpoint left client socket readable")
	}
	deadline = time.Now().Add(time.Second)
	for len(m.Snapshot()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(m.Snapshot()); got != 0 {
		t.Fatalf("remaining connections = %d, want 0", got)
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

// TestManagerReRegisterKeepsEndpointConnectionLimit — apid's reconciler
// replays identical endpoint intent. RegisterEndpoint used to store a fresh
// endpointState whose connection counter started at zero while the open
// sockets kept releasing against the old one, so every re-registration
// admitted another MaxConnections.
func TestManagerReRegisterKeepsEndpointConnectionLimit(t *testing.T) {
	manager := NewManager(Config{}, nil)
	defer func() { _ = manager.Close() }()
	endpoint := Endpoint{ID: "ep", AppID: "app", AccountID: "acct", MaxConnections: 1}
	if err := manager.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + ManagedPathPrefix + "ep"

	first, _, err := dialClosingResponse(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	for round := 0; round < 3; round++ {
		if err := manager.RegisterEndpoint(endpoint); err != nil {
			t.Fatal(err)
		}
		extra, status, err := dialClosingResponse(url)
		if err == nil {
			_ = extra.Close()
			t.Fatalf("round %d: max_connections=1 admitted a second socket after re-registration", round)
		}
		if status != http.StatusTooManyRequests {
			t.Fatalf("round %d: second dial err=%v status=%d, want 429", round, err, status)
		}
	}

	// Closing the original socket releases the shared counter, so a new
	// connection is admitted again under the re-registered definition.
	_ = first.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		next, _, err := dialClosingResponse(url)
		if err == nil {
			_ = next.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never released after the first socket closed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dialClosingResponse dials url and closes the handshake response body,
// returning the handshake status (0 when there was no response).
func dialClosingResponse(url string) (*websocket.Conn, int, error) {
	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	status := 0
	if response != nil {
		status = response.StatusCode
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}
	return conn, status, err
}
