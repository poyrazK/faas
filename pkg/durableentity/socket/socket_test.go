package socket

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSocketRoutesFramesInOrderAndReleasesCapacity(t *testing.T) {
	hub := New()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.Serve(w, r, "account", "entity", func(f Frame) Reply {
			calls++
			return Reply{Status: 200, Result: &api.DurableEntityInvokeResponse{Value: json.RawMessage(`{"count":1}`), Version: uint64(calls)}}
		})
	}))
	defer server.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil {
		response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, id := range []string{"one", "two"} {
		if err := conn.WriteJSON(Frame{ProtocolVersion: 1, RequestID: id, Payload: json.RawMessage(`{"delta":1}`)}); err != nil {
			t.Fatal(err)
		}
		var reply Reply
		if err := conn.ReadJSON(&reply); err != nil {
			t.Fatal(err)
		}
		if reply.RequestID != id || reply.Status != 200 {
			t.Fatal(reply)
		}
	}
	_ = conn.Close()
}
func TestSocketLimiterAndMalformedFrame(t *testing.T) {
	hub := New()
	releases := []func(){}
	for range MaxEntityConnections {
		release, ok := hub.acquire("a", "e")
		if !ok {
			t.Fatal("premature capacity rejection")
		}
		releases = append(releases, release)
	}
	if _, ok := hub.acquire("a", "e"); ok {
		t.Fatal("entity capacity exceeded")
	}
	for _, release := range releases {
		release()
	}
	release, ok := hub.acquire("a", "e")
	if !ok {
		t.Fatal("slots leaked")
	}
	release()
	for _, body := range []string{`{"protocol_version":1,"request_id":"one"}`, `{"protocol_version":2,"request_id":"one","payload":null}`, `{"protocol_version":1,"request_id":"__gregale_alarm/1","payload":null}`, `{"protocol_version":1,"request_id":"one","payload":null,"extra":true}`} {
		var frame Frame
		if decodeFrame([]byte(body), &frame) == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
func TestSocketRejectsBrowserOriginsBeforeUpgrade(t *testing.T) {
	hub := New()
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Origin", "https://example.test")
	response := httptest.NewRecorder()
	hub.Serve(response, request, "a", "e", func(Frame) Reply { t.Fatal("origin reached invocation"); return Reply{} })
	if response.Code == http.StatusSwitchingProtocols || hub.total != 0 {
		t.Fatal("browser admission or leaked slot")
	}
}
