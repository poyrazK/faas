package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A handler that works longer than one stall timeout before writing must still
// deliver its response: the deadline guards each write, not the whole request.
func TestPerWriteDeadlineAllowsSlowHandlers(t *testing.T) {
	previous := writeStallTimeout
	writeStallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { writeStallTimeout = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = newPerWriteDeadline(w)
		time.Sleep(4 * writeStallTimeout)
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		time.Sleep(4 * writeStallTimeout)
		_, _ = w.Write([]byte(" second"))
	}))
	t.Cleanup(server.Close)
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "first second" {
		t.Fatalf("slow handler response = %q, %v", body, err)
	}
}

// http.ResponseController must still reach the connection through the wrapper.
func TestPerWriteDeadlineUnwrapsForResponseController(t *testing.T) {
	recorder := httptest.NewRecorder()
	wrapped := newPerWriteDeadline(recorder)
	if err := http.NewResponseController(wrapped).Flush(); err != nil {
		t.Fatalf("flush through wrapper: %v", err)
	}
	if !recorder.Flushed {
		t.Fatal("wrapper did not flush the underlying writer")
	}
}
