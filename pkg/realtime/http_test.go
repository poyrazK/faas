package realtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
