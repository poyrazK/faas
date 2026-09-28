package realtime

// adr: 296
// adr: 297

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestHTTPHooksUsesRotatedTokenForNewDurableCallback(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	if err := queue.updateCallbackAuthToken("endpoint-1", "callback-secret-next"); err != nil {
		t.Fatalf("update callback token: %v", err)
	}
	event := testCallbackEvent()
	event.CallbackURL = server.URL
	if err := (HTTPHooks{Client: server.Client(), DurableQueue: queue}).Message(context.Background(), event); err != nil {
		t.Fatalf("Message callback: %v", err)
	}
	if authorization != "Bearer callback-secret-next" {
		t.Fatalf("Authorization = %q, want rotated callback token", authorization)
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

func TestCallbackRetryAfterParsesOnlyBoundedRetryableResponses(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if got := parseCallbackRetryAfter(http.StatusTooManyRequests, "12", now); got != 12*time.Second {
		t.Errorf("Retry-After seconds = %s, want 12s", got)
	}
	date := now.Add(30 * time.Second).Format(http.TimeFormat)
	if got := parseCallbackRetryAfter(http.StatusServiceUnavailable, date, now); got != 30*time.Second {
		t.Errorf("Retry-After date = %s, want 30s", got)
	}
	if got := parseCallbackRetryAfter(http.StatusServiceUnavailable, "999999999", now); got != MaxCallbackOutboxMaxRetryInterval {
		t.Errorf("oversized Retry-After = %s, want cap %s", got, MaxCallbackOutboxMaxRetryInterval)
	}
	if got := parseCallbackRetryAfter(http.StatusServiceUnavailable, "18446744073709551615", now); got != MaxCallbackOutboxMaxRetryInterval {
		t.Errorf("Retry-After above int64 range = %s, want cap %s", got, MaxCallbackOutboxMaxRetryInterval)
	}
	if got := parseCallbackRetryAfter(http.StatusServiceUnavailable, "-999999999", now); got != 0 {
		t.Errorf("negative Retry-After = %s, want 0", got)
	}
	if got := parseCallbackRetryAfter(http.StatusBadGateway, "12", now); got != 0 {
		t.Errorf("Retry-After on 502 = %s, want ignored", got)
	}
	if got := parseCallbackRetryAfter(http.StatusTooManyRequests, "invalid", now); got != 0 {
		t.Errorf("invalid Retry-After = %s, want ignored", got)
	}
}

func TestHTTPHooksPersistsBoundedRetryAfterOnDurableFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{
		Root: root, RetryInterval: time.Millisecond, MaxRetryInterval: 20 * time.Second,
	})
	hooks := HTTPHooks{Client: server.Client(), DurableQueue: queue, MaxAttempts: 3}
	event := testCallbackEvent()
	event.CallbackURL = server.URL
	started := time.Now()
	err := hooks.Message(context.Background(), event)
	var callbackErr *CallbackHTTPError
	if !errors.As(err, &callbackErr) || callbackErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Message error = %v, want CallbackHTTPError(503)", err)
	}
	if callbackErr.RetryAfter != 60*time.Second {
		t.Fatalf("Retry-After hint = %s, want 60s before queue cap", callbackErr.RetryAfter)
	}
	payload, err := os.ReadFile(filepath.Join(root, event.ID+".json"))
	if err != nil {
		t.Fatalf("read pending callback: %v", err)
	}
	var record callbackOutboxRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatalf("decode pending callback: %v", err)
	}
	remaining := record.NextAttemptAt.Sub(started)
	if record.Attempts != 1 || remaining < 19*time.Second || remaining > 21*time.Second {
		t.Fatalf("pending retry = attempts %d, remaining %s, want 20s configured cap", record.Attempts, remaining)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("callback requests = %d, want one because 60s Retry-After exceeds inline cap", got)
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

func TestHTTPHooksWaitsForOutboxCapacityAndStoresTheBlockedEvent(t *testing.T) {
	delivered := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		delivered <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{RetryInterval: time.Millisecond})
	blocker := testCallbackEvent()
	blocker.ID = "evt_blocker"
	blocker.ConnectionID = "conn-blocker"
	blocker.CallbackURL = server.URL
	if claimed, err := queue.EnqueueAndClaim(blocker); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim blocker = (%v, %v)", claimed, err)
	}
	queue.mu.Lock()
	queue.maxBytes = queue.items[blocker.ID].size
	queue.mu.Unlock()

	event := testCallbackEvent()
	event.ID = "evt_pending"
	event.ConnectionID = "conn-pending"
	event.CallbackURL = server.URL
	hooks := HTTPHooks{Client: server.Client(), DurableQueue: queue, MaxAttempts: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	messageErr := make(chan error, 1)
	go func() { messageErr <- hooks.Message(ctx, event) }()

	select {
	case body := <-delivered:
		t.Fatalf("callback delivered before capacity was freed: %s", body)
	case <-time.After(10 * time.Millisecond):
	}
	if err := queue.Ack(blocker.ID); err != nil {
		t.Fatalf("Ack blocker: %v", err)
	}
	select {
	case err := <-messageErr:
		if err != nil {
			t.Fatalf("Message after capacity was freed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Message did not finish after outbox capacity was freed")
	}
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("blocked event was not delivered after it was persisted")
	}
	if stats := queue.Stats(); stats.Pending != 0 {
		t.Fatalf("queue Stats after delivery = %+v, want empty", stats)
	}
}

func TestHTTPHooksReturnsOutboxFullWhenCapacityDoesNotRecoverBeforeDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxBytes: 1, RetryInterval: time.Millisecond})
	event := testCallbackEvent()
	event.CallbackURL = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := (HTTPHooks{Client: server.Client(), DurableQueue: queue}).Message(ctx, event)
	if !errors.Is(err, ErrCallbackOutboxAdmission) || !errors.Is(err, ErrCallbackOutboxFull) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Message error = %v, want admission failure, outbox full, and deadline exceeded", err)
	}
}

func TestHTTPHooksMarksOutboxPersistenceFailureAsAdmissionFailure(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	queue.root += "-unavailable"
	event := testCallbackEvent()
	event.CallbackURL = "https://example.com"

	err := (HTTPHooks{DurableQueue: queue}).Message(context.Background(), event)
	if !errors.Is(err, ErrCallbackOutboxAdmission) {
		t.Fatalf("Message error = %v, want ErrCallbackOutboxAdmission", err)
	}
	if errors.Is(err, ErrCallbackOutboxFull) {
		t.Fatalf("Message error = %v, unexpectedly wraps ErrCallbackOutboxFull", err)
	}
	if got := queue.Stats().Pending; got != 0 {
		t.Fatalf("pending callbacks = %d, want no event admitted", got)
	}
}

func TestHTTPHooksMarksNonDurableMessageAndDisconnectFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	hooks := HTTPHooks{Client: server.Client(), MaxAttempts: 1}

	message := testCallbackEvent()
	message.CallbackURL = server.URL
	message.CallbackPath = "/message"
	if err := hooks.Message(context.Background(), message); !errors.Is(err, ErrCallbackNotPersisted) {
		t.Fatalf("Message error = %v, want ErrCallbackNotPersisted", err)
	}

	disconnect := message
	disconnect.ID = "evt_disconnect"
	disconnect.Type = EventDisconnect
	disconnect.CallbackPath = "/disconnect"
	if err := hooks.Disconnect(context.Background(), disconnect); !errors.Is(err, ErrCallbackNotPersisted) {
		t.Fatalf("Disconnect error = %v, want ErrCallbackNotPersisted", err)
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

	for _, path := range []string{"/internal/stats", "/internal/connections", "/internal/endpoints", "/internal/callbacks/dead-letters"} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("health handler %s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}
