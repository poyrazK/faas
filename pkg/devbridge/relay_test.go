package devbridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRelayTwoLaptopsStreamAndRevokeIndependently(t *testing.T) {
	relay := NewRelay(4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sessions := map[string]Session{}
	for _, id := range []string{"alice", "bob"} {
		sessions[id] = Session{ID: id, ExpiresAt: time.Now().Add(time.Minute)}
	}
	var attach sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		attach.Add(1)
		defer attach.Done()
		_ = relay.Attach(ctx, sessions[r.URL.Path[1:]], NewWebSocketConn(socket))
	}))
	defer func() {
		cancel()
		relay.CloseSession("alice")
		relay.CloseSession("bob")
		server.Close()
		attach.Wait()
	}()
	for _, id := range []string{"alice", "bob"} {
		local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = http.NewResponseController(w).EnableFullDuplex()
			if r.Header.Get(TokenHeader) != "" || r.Header.Get(SessionHeader) != "" {
				t.Error("bridge credentials reached local process")
			}
			w.Header().Set("X-Developer", id)
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			_, _ = io.Copy(w, r.Body)
		}))
		defer local.Close()
		target, _ := url.Parse(local.URL)
		socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/"+id, nil)
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = ServeLocal(ctx, NewWebSocketConn(socket), target, 4) }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for !relay.Connected("alice") || !relay.Connected("bob") {
		if time.Now().After(deadline) {
			t.Fatal("laptops did not attach")
		}
		time.Sleep(time.Millisecond)
	}
	for _, id := range []string{"alice", "bob"} {
		// The response must arrive before the upload ends: this verifies full
		// duplex streaming rather than a whole-body buffered relay.
		reader, writer := io.Pipe()
		request, _ := http.NewRequestWithContext(ctx, "POST", "https://bridge.invalid/echo", reader)
		request.Header.Set(TokenHeader, "secret")
		request.Header.Set(SessionHeader, "session")
		response, err := relay.RoundTrip(id, request)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("X-Developer") != id {
			t.Fatal("request reached another developer")
		}
		payload := strings.Repeat("streamed", 10000)
		go func() { _, _ = io.WriteString(writer, payload); _ = writer.Close() }()
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != payload {
			t.Fatalf("stream body: len=%d err=%v", len(body), err)
		}
	}
	relay.CloseSession("alice")
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://bridge.invalid/", nil)
	if _, err := relay.RoundTrip("alice", req); err != ErrDisconnected {
		t.Fatalf("revoked laptop dispatched: %v", err)
	}
	response, err := relay.RoundTrip("bob", req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestServeLocalRejectsNonLoopback(t *testing.T) {
	// Validation runs before serving a tunnel. No destination chosen by a
	// remote peer is permitted, including credentials or a query-bearing URL.
	for _, raw := range []string{"http://example.com:8080", "https://127.0.0.1:8080", "http://user:pass@127.0.0.1:8080", "http://127.0.0.1:8080?destination=other"} {
		target, _ := url.Parse(raw)
		a, b := net.Pipe()
		if ServeLocal(t.Context(), a, target, 1) == nil {
			t.Fatalf("accepted %s", raw)
		}
		_ = b.Close()
	}
}
