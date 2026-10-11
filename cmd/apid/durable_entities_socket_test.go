package main

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDurableEntitySocketReplaysAfterReconnectAndRechecksAppAdmission(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, false)
	enabled := true
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWebSocketEnabled: true, WebSocketEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	e.s.durableEntitySockets = entitysocket.New()
	server := httptest.NewServer(e.h)
	defer server.Close()
	dial := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/apps/"+app.Slug+"/entities/connect?namespace=counters&key=customer:456", http.Header{"Authorization": []string{"Bearer " + e.key}})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	frame := entitysocket.Frame{ProtocolVersion: 1, RequestID: "socket-one", Payload: json.RawMessage(`{"delta":1}`)}
	conn := dial()
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
	var first entitysocket.Reply
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatal(err)
	}
	if first.Status != 200 || first.Result == nil || first.Result.Replayed {
		t.Fatal(first)
	}
	_ = conn.Close()
	conn = dial()
	defer func() { _ = conn.Close() }()
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
	var replay entitysocket.Reply
	if err := conn.ReadJSON(&replay); err != nil {
		t.Fatal(err)
	}
	if replay.Status != 200 || replay.Result == nil || !replay.Result.Replayed || dispatch.calls.Load() != 1 {
		t.Fatalf("reconnect did not replay: %+v", replay)
	}
	enabled = false
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWebSocketEnabled: true, WebSocketEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	frame.RequestID = "socket-two"
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
	var denied entitysocket.Reply
	if err := conn.ReadJSON(&denied); err != nil {
		t.Fatal(err)
	}
	if denied.Status != 403 || dispatch.calls.Load() != 1 {
		t.Fatalf("changed admission dispatched: %+v", denied)
	}
}
func TestDurableEntitySocketRequiresExplicitPreview(t *testing.T) {
	e, app, _, _ := entityAPIFixture(t, false)
	response := e.do(t, "GET", "/v1/apps/"+app.Slug+"/entities/connect?namespace=counters&key=a", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled socket status %d", response.Code)
	}
}

func TestDurableEntitySocketRevokedCredentialCannotDispatchNextFrame(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, false)
	enabled := true
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWebSocketEnabled: true, WebSocketEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	e.s.durableEntitySockets = entitysocket.New()
	server := httptest.NewServer(e.h)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/apps/"+app.Slug+"/entities/connect?namespace=counters&key=customer:456", http.Header{"Authorization": []string{"Bearer " + e.key}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	keys, err := e.store.ListAPIKeys(t.Context(), e.acct.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys: %v", err)
	}
	if _, err := e.store.DeleteAPIKeyReturning(t.Context(), e.acct.ID, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(entitysocket.Frame{ProtocolVersion: 1, RequestID: "revoked", Payload: json.RawMessage(`{"delta":1}`)}); err != nil {
		t.Fatal(err)
	}
	var denied entitysocket.Reply
	if err := conn.ReadJSON(&denied); err != nil {
		t.Fatal(err)
	}
	if denied.Status != 401 || dispatch.calls.Load() != 0 {
		t.Fatalf("revoked credential dispatched: %+v", denied)
	}
}
func TestDurableEntitySocketFlagRequiresInvocationPreview(t *testing.T) {
	e := entityAPISetup(t)
	err := e.s.configureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_SOCKETS_ENABLED" {
			return "1"
		}
		return ""
	})
	if err == nil || e.s.durableEntitySockets != nil {
		t.Fatal("socket flag enabled without invocation preview")
	}
}
