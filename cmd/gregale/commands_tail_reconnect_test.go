package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Production: apid closed /v1/events five seconds after it opened, and
// `gregale tail` treated the EOF as success and exited. A tail is a session:
// when the server ends the stream it reconnects until Ctrl-C.
func TestGregaleTail_ReconnectsWhenStreamEnds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	prevMin, prevMax := tailReconnectMin, tailReconnectMax
	tailReconnectMin, tailReconnectMax = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { tailReconnectMin, tailReconnectMax = prevMin, prevMax })

	var connections atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_, _ = w.Write([]byte("[]"))
		case "/v1/events":
			n := connections.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			// One frame per connection, then end the stream like apid's
			// budget cut did.
			_, _ = fmt.Fprintf(w, "event: invocation_done\ndata: {\"invocation_id\":\"i-%d\",\"app_id\":\"a1\",\"state\":\"completed\"}\n\n", n)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	stdout, restore := captureStdout(t)
	defer restore()
	done := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("cmdTail did not stop during cleanup")
			}
		}
	}()
	go func() { done <- cmdTailContext(ctx, nil) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdout.String(), "i-3 a1 completed") {
		select {
		case code := <-done:
			finished = true
			t.Fatalf("cmdTail exited with %d after the stream ended; want it to reconnect. stdout=%q", code, stdout.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !strings.Contains(stdout.String(), "i-1 a1 completed") || !strings.Contains(stdout.String(), "i-3 a1 completed") {
		t.Fatalf("frames across reconnects missing; stdout=%q", stdout.String())
	}
	cancel()
	select {
	case code := <-done:
		finished = true
		if code != 130 {
			t.Fatalf("cmdTail exit = %d, want 130", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cmdTail did not exit on cancellation")
	}
}
