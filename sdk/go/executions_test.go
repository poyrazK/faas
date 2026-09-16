package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func writeExecutionStream(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func TestWatchExecutionResumesAfterEOF(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		call++
		current := call
		mu.Unlock()
		if r.URL.Path != "/v1/executions/run-1/events" {
			http.NotFound(w, r)
			return
		}
		if current == 1 {
			writeExecutionStream(w,
				"id: 1\nevent: status\ndata: {\"status\":\"running\"}\n\n"+
					"id: 2\nevent: stdout\ndata: {\"chunk\":\"hello\\n\"}\n\n")
			return
		}
		writeExecutionStream(w, "id: 3\nevent: terminal\ndata: {\"status\":\"succeeded\",\"exit_code\":0}\n\n")
	}))
	defer srv.Close()

	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	watcher := c.WatchExecution(context.Background(), "run-1", faas.WatchExecutionOptions{
		RetryInitial: 0,
		RetryMax:     0,
		Limit:        25,
	})
	defer watcher.Close()

	var events []faas.ExecutionEvent
	for {
		event, nextErr := watcher.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("Next: %v", nextErr)
		}
		events = append(events, event)
	}
	if got, want := len(events), 3; got != want {
		t.Fatalf("events: got %d, want %d", got, want)
	}
	if events[0].Type != faas.ExecutionEventStatus || events[0].Data.Status != faas.ExecutionStatusRunning {
		t.Fatalf("status event: %+v", events[0])
	}
	if events[1].Type != faas.ExecutionEventStdout || events[1].Data.Chunk != "hello\n" {
		t.Fatalf("stdout event: %+v", events[1])
	}
	if events[2].Type != faas.ExecutionEventTerminal || events[2].Data.ExitCode == nil || *events[2].Data.ExitCode != 0 {
		t.Fatalf("terminal event: %+v", events[2])
	}
	if got := watcher.Cursor(); got != 3 {
		t.Errorf("Cursor: got %d, want 3", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("stream requests: got %d, want 2 (%v)", len(requests), requests)
	}
	if requests[0] != "/v1/executions/run-1/events?limit=25" {
		t.Errorf("initial request: got %q", requests[0])
	}
	if requests[1] != "/v1/executions/run-1/events?after=2&limit=25" {
		t.Errorf("resume request: got %q", requests[1])
	}
}

func TestWatchExecutionRejectsMalformedPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExecutionStream(w, "id: 1\nevent: status\ndata: not-json\n\n")
	}))
	defer srv.Close()
	c, _ := faas.NewClient(srv.URL, "token")
	watcher := c.WatchExecution(context.Background(), "run-1", faas.WatchExecutionOptions{})
	defer watcher.Close()
	_, err := watcher.Next()
	if err == nil {
		t.Fatal("Next returned nil error for malformed payload")
	}
	var parseErr *faas.ExecutionEventParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("error = %T %v, want ExecutionEventParseError", err, err)
	}
}

func TestRunStreamsAndReturnsFinalReceipt(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/executions":
			var request faas.CreateExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			if request.Runtime != faas.ExecutionRuntimeNode22 || request.Source == "" {
				t.Errorf("create request: %+v", request)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"run-1","status":"queued","runtime":"node22","limits":{"timeout_ms":1000},"output_truncated":false,"created_at":"2026-01-01T00:00:00Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/executions/run-1/events":
			writeExecutionStream(w, "id: 1\nevent: status\ndata: {\"status\":\"running\"}\n\n"+
				"id: 2\nevent: stdout\ndata: {\"chunk\":\"hello\"}\n\n"+
				"id: 3\nevent: terminal\ndata: {\"status\":\"succeeded\",\"exit_code\":0}\n\n")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/executions/run-1":
			_, _ = io.WriteString(w, `{"id":"run-1","status":"succeeded","runtime":"node22","limits":{"timeout_ms":1000},"result":{"ok":true},"stdout":"hello","output_truncated":false,"exit_code":0,"created_at":"2026-01-01T00:00:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := faas.NewClient(srv.URL, "token")
	var seen []faas.ExecutionEventType
	receipt, err := c.Run(context.Background(), faas.CreateExecutionRequest{
		Runtime: faas.ExecutionRuntimeNode22,
		Source:  "console.log('hello')",
	}, faas.RunOptions{
		Watch: faas.WatchExecutionOptions{RetryInitial: 0, RetryMax: 0},
		OnEvent: func(event faas.ExecutionEvent) error {
			seen = append(seen, event.Type)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if receipt.Status != faas.ExecutionStatusSucceeded || receipt.Result == nil {
		t.Fatalf("receipt: %+v", receipt)
	}
	if len(seen) != 3 || seen[2] != faas.ExecutionEventTerminal {
		t.Fatalf("events: %v", seen)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 3 || methods[0] != "POST /v1/executions" || methods[1] != "GET /v1/executions/run-1/events" || methods[2] != "GET /v1/executions/run-1" {
		t.Errorf("request order: %v", methods)
	}
}
