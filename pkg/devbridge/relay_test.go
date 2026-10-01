package devbridge

import (
	"context"
	"errors"
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
		socket, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/"+id, nil)
		if handshake != nil && handshake.Body != nil {
			_ = handshake.Body.Close()
		}
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
	response, err := relay.RoundTrip("alice", req)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, ErrDisconnected) {
		t.Fatalf("revoked laptop dispatched: %v", err)
	}
	response, err = relay.RoundTrip("bob", req)
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

func TestRelayReconnectFencesOldConnectionAndCancelsRequests(t *testing.T) {
	relay := NewRelay(2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session := Session{ID: "alice", ExpiresAt: time.Now().Add(time.Minute)}
	var tasks sync.WaitGroup
	defer func() { cancel(); relay.CloseSession(session.ID); tasks.Wait() }()
	attach := func(target *url.URL) <-chan error {
		t.Helper()
		a, b := net.Pipe()
		done := make(chan error, 1)
		tasks.Add(2)
		go func() { defer tasks.Done(); done <- relay.Attach(ctx, session, a) }()
		go func() { defer tasks.Done(); _ = ServeLocal(ctx, b, target, 2) }()
		return done
	}
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "old") }))
	defer first.Close()
	target, _ := url.Parse(first.URL)
	oldDone := attach(target)
	waitFor := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatal("connection did not become ready")
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitFor(func() bool { return relay.Connected(session.ID) })
	started, canceled := make(chan struct{}), make(chan struct{})
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel" {
			close(started)
			<-r.Context().Done()
			close(canceled)
			return
		}
		_, _ = io.WriteString(w, "new")
	}))
	defer second.Close()
	target, _ = url.Parse(second.URL)
	_ = attach(target)
	select {
	case <-oldDone:
	case <-time.After(3 * time.Second):
		t.Fatal("old connection did not stop")
	}
	waitFor(func() bool { return relay.Connected(session.ID) })
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://bridge.invalid/", nil)
	response, err := relay.RoundTrip(session.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "new" {
		t.Fatalf("replacement lost: %q", body)
	}
	requestCtx, requestCancel := context.WithCancel(ctx)
	request, _ = http.NewRequestWithContext(requestCtx, "GET", "http://bridge.invalid/cancel", nil)
	finished := make(chan error, 1)
	go func() {
		response, err := relay.RoundTrip(session.ID, request)
		if response != nil {
			_ = response.Body.Close()
		}
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not arrive")
	}
	requestCancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("caller cancellation was lost")
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("local request cancellation was lost")
	}
}
