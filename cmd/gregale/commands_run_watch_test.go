package main

// spec: §4.4
// adr: 171

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const executionWatchTestID = "01234567-89ab-4cde-8012-3456789abcde"

func TestCmdRunWatchJSONReconnectsWithCursor(t *testing.T) {
	var eventStreams atomic.Int32
	srv := newExecutionWatchTestServer(t, &eventStreams)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "watch-token")

	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	out, _, restore := swapIO(t)
	defer restore()

	if got := cmdRun([]string{"--runtime", "node24", "--source", "console.log(1)", "--watch"}); got != 0 {
		t.Fatalf("cmdRun exit = %d, output=%q", got, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("watch output lines = %d, output=%q", len(lines), out.String())
	}
	var events []executionWatchEvent
	for _, line := range lines {
		var event executionWatchEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("event JSON %q: %v", line, err)
		}
		events = append(events, event)
	}
	if events[0].Type != "status" || events[1].Type != "stdout" || events[2].Type != "stderr" || events[3].Type != "terminal" {
		t.Fatalf("event types = %#v", []string{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	}
	if events[1].ID != "2" || events[2].ID != "3" || events[3].ID != "4" {
		t.Fatalf("event ids = %#v", []string{events[1].ID, events[2].ID, events[3].ID})
	}
	if events[3].Receipt == nil || events[3].Receipt.Result == nil || string(events[3].Receipt.Result) != `{"answer":42}` {
		t.Fatalf("terminal receipt = %#v", events[3].Receipt)
	}
	if got := eventStreams.Load(); got != 2 {
		t.Fatalf("event stream requests = %d, want 2", got)
	}
}

func TestCmdRunWatchHumanDoesNotDuplicateOutput(t *testing.T) {
	var eventStreams atomic.Int32
	srv := newExecutionWatchTestServer(t, &eventStreams)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "watch-token")

	oldJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = oldJSON })
	out, errOut, restore := swapIO(t)
	defer restore()

	if got := cmdRun([]string{"--runtime", "node24", "--source", "console.log(1)", "--watch"}); got != 0 {
		t.Fatalf("cmdRun exit = %d, stdout=%q, stderr=%q", got, out.String(), errOut())
	}
	if got := strings.Count(out.String(), "hello\n"); got != 1 {
		t.Fatalf("stdout chunk count = %d, stdout=%q", got, out.String())
	}
	if got := strings.Count(errOut(), "warning\n"); got != 1 {
		t.Fatalf("stderr chunk count = %d, stderr=%q", got, errOut())
	}
	if !strings.Contains(out.String(), "Run 01234567-89ab-4cde-8012-3456789abcde succeeded.") {
		t.Fatalf("terminal summary missing: %q", out.String())
	}
}

func newExecutionWatchTestServer(t *testing.T, eventStreams *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/v1/executions/" + executionWatchTestID
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/executions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fmt.Sprintf(`{"id":%q,"status":"queued","runtime":"node24","created_at":"2026-01-01T00:00:00Z"}`, executionWatchTestID))
		case r.Method == http.MethodGet && r.URL.Path == prefix+"/events":
			stream := eventStreams.Add(1)
			if stream == 2 && r.URL.Query().Get("after") != "2" {
				t.Fatalf("reconnect cursor = %q, want 2", r.URL.Query().Get("after"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if stream == 1 {
				_, _ = io.WriteString(w, "id: 1\nevent: status\ndata: {\"status\":\"running\"}\n\n")
				_, _ = io.WriteString(w, "id: 2\nevent: stdout\ndata: {\"chunk\":\"hello\\n\"}\n\n")
				return
			}
			_, _ = io.WriteString(w, "id: 3\nevent: stderr\ndata: {\"chunk\":\"warning\\n\"}\n\n")
			_, _ = io.WriteString(w, "id: 4\nevent: terminal\ndata: {\"status\":\"succeeded\",\"output_truncated\":false}\n\n")
		case r.Method == http.MethodGet && r.URL.Path == prefix:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fmt.Sprintf(`{"id":%q,"status":"succeeded","runtime":"node24","result":{"answer":42},"stdout":"hello\\n","stderr":"warning\\n","output_truncated":false,"created_at":"2026-01-01T00:00:00Z"}`, executionWatchTestID))
		default:
			http.NotFound(w, r)
		}
	}))
}
