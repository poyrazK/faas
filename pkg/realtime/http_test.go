package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPHooksSendsCallbackAuthAndOmitsSecretsFromEvent(t *testing.T) {
	var received Event
	var raw string
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		raw = string(body)
		_ = json.Unmarshal(body, &received)
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	event := Event{
		ID:                "evt_1",
		Type:              EventMessage,
		ConnectionID:      "rt_1",
		Data:              []byte("hello"),
		CallbackURL:       server.URL,
		CallbackPath:      "/events",
		CallbackAuthToken: "callback-secret",
	}
	if err := (HTTPHooks{}).Message(context.Background(), event); err != nil {
		t.Fatalf("Message callback: %v", err)
	}
	if authorization != "Bearer callback-secret" {
		t.Fatalf("Authorization = %q, want bearer callback token", authorization)
	}
	if received.ID != event.ID || received.ConnectionID != event.ConnectionID || string(received.Data) != "hello" {
		t.Fatalf("received event = %+v", received)
	}
	if strings.Contains(raw, "callback-secret") || strings.Contains(raw, server.URL) {
		t.Fatalf("callback secrets leaked into event JSON: %s", raw)
	}
}

func TestHTTPHooksRetriesTransientCallbackFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	hooks := HTTPHooks{MaxAttempts: 3, RetryBackoff: time.Millisecond}
	event := Event{ID: "evt_retry", Type: EventMessage, ConnectionID: "rt_retry", CallbackURL: server.URL, CallbackPath: "/events"}
	if err := hooks.Message(context.Background(), event); err != nil {
		t.Fatalf("Message callback: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("callback attempts = %d, want 3", got)
	}
}

func TestHTTPHooksDoesNotRetryTerminalCallbackFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	hooks := HTTPHooks{MaxAttempts: 3, RetryBackoff: time.Millisecond}
	event := Event{ID: "evt_terminal", Type: EventMessage, ConnectionID: "rt_terminal", CallbackURL: server.URL, CallbackPath: "/events"}
	if err := hooks.Message(context.Background(), event); err == nil {
		t.Fatal("Message callback unexpectedly succeeded")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("callback attempts = %d, want 1", got)
	}
}

func TestHTTPHooksDurableMessageReplaysAfterInitialFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	hooks := HTTPHooks{Client: server.Client(), DurableQueue: queue, MaxAttempts: 1}
	event := Event{ID: "evt_durable", Type: EventMessage, ConnectionID: "rt_durable", CallbackURL: server.URL, CallbackPath: "/events"}
	if err := hooks.Message(context.Background(), event); err == nil {
		t.Fatal("Message callback unexpectedly succeeded on transient failure")
	}
	if stats := queue.Stats(); stats.Pending != 1 {
		t.Fatalf("queue Stats after failed callback = %+v, want one pending event", stats)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(deliveryCtx context.Context, pending Event) error {
			err := hooks.Deliver(deliveryCtx, pending)
			if err == nil {
				cancel()
			}
			return err
		})
	}()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("callback attempts = %d, want initial attempt plus replay", got)
	}
	if stats := queue.Stats(); stats.Pending != 0 {
		t.Fatalf("queue Stats after replay = %+v, want empty", stats)
	}
}

func TestHTTPHooksRetryBackoffHonorsContext(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	hooks := HTTPHooks{MaxAttempts: 3, RetryBackoff: time.Hour}
	go func() {
		for attempts.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	event := Event{ID: "evt_cancel", Type: EventMessage, ConnectionID: "rt_cancel", CallbackURL: server.URL, CallbackPath: "/events"}
	if err := hooks.Message(ctx, event); err == nil {
		t.Fatal("Message callback unexpectedly succeeded")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("callback attempts = %d, want 1 before cancellation", got)
	}
}

func TestHealthHandlerDoesNotExposeManagementRoutes(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	handler := m.HealthHandler()

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", recorder.Code, http.StatusOK)
	}

	for _, path := range []string{"/internal/stats", "/internal/connections", "/internal/endpoints"} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("health handler %s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}
