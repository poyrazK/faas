package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestFormatDevRuntimeLog(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "structured stdout",
			data: `{"seq":1,"stream":"stdout","line":"ready"}`,
			want: "runtime stdout | ready",
		},
		{
			name: "structured stderr",
			data: `{"seq":2,"stream":"stderr","line":"failed"}`,
			want: "runtime stderr | failed",
		},
		{
			name: "plain fallback",
			data: "plain application output",
			want: "runtime | plain application output",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := main.FormatDevRuntimeLog(test.data); got != test.want {
				t.Fatalf("formatDevRuntimeLog(%q) = %q, want %q", test.data, got, test.want)
			}
		})
	}
}

func TestStreamDevRuntimeLogsRendersAppStream(t *testing.T) {
	main.ResetJSONOut(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/logs" || r.URL.Query().Get("follow") != "1" {
			t.Errorf("request = %s?%s, want app log follow stream", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		_, _ = w.Write([]byte("event: log\ndata: {\"seq\":1,\"stream\":\"stdout\",\"line\":\"ready\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: log\ndata: {\"seq\":2,\"stream\":\"stderr\",\"line\":\"warning\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	oldOut := (*main.OsStdout)
	var output strings.Builder
	(*main.OsStdout) = &output
	defer func() { (*main.OsStdout) = oldOut }()

	if err := main.StreamDevRuntimeLogs(context.Background(), api.NewClient(server.URL, "token"), "demo"); err != nil {
		t.Fatalf("streamDevRuntimeLogs: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "runtime stdout | ready\n") || !strings.Contains(got, "runtime stderr | warning\n") {
		t.Fatalf("runtime output = %q, want both structured lines", got)
	}
}

func TestDevRuntimeJSONDiagnosticsReconnectAndStop(t *testing.T) {
	main.ResetJSONOut(t)
	*main.JsonOutput = true
	out, restore := main.CaptureStdout(t)
	defer restore()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		if r.URL.Path != "/v1/apps/dev-api-workspace/logs" || r.URL.Query().Get("follow") != "1" {
			t.Errorf("unexpected stream target: %s", r.URL)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		log := func(line string) {
			data, _ := json.Marshal(api.LogEvent{Line: line, Stream: "stderr"})
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
		log("Local: http://127.0.0.1:5173")
		log("connect ECONNREFUSED 127.0.0.1:5432")
		log("connect ECONNREFUSED 127.0.0.1:5432")
		if call == 1 {
			_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			return
		}
		log("panic: application failed")
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	received := make(chan string, 8)
	report := main.NewDevDiagnosticReporterForTest("/source/api", "dev-api-workspace")
	stop := main.StartDevRuntimeLogsForTest(ctx, api.NewClient(server.URL, "token"), "dev-api-workspace", false, false, func(d main.DevDiagnostic) {
		report(d)
		received <- d.Code
	})
	defer stop()
	for _, want := range []string{main.DevDiagUpstreamFailure, main.DevDiagStartupCrash} {
		select {
		case code := <-received:
			if code != want {
				t.Fatalf("diagnostic code=%s, want %s", code, want)
			}
		case <-ctx.Done():
			t.Fatal("runtime diagnostics did not survive a reconnect")
		}
	}
	stop()
	if requests.Load() != 2 {
		t.Fatalf("stream requests=%d, want 2", requests.Load())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("raw output or repeated diagnostics in JSON stream: %q", out.String())
	}
	for _, line := range lines {
		var d main.DevDiagnostic
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("invalid NDJSON: %v: %s", err, line)
		}
		if d.Event != "developer_diagnostic" || d.Phase != "runtime" || d.LogsCommand != "gregale logs --app dev-api-workspace --source runtime" {
			t.Fatalf("runtime context=%+v", d)
		}
	}
}

func TestDevRuntimeLogsRespectOnceAndNoLogs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		once, noLogs bool
	}{
		{name: "once", once: true},
		{name: "no-logs", noLogs: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			*main.JsonOutput = true
			requested := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requested <- struct{}{}
			}))
			defer server.Close()
			stop := main.StartDevRuntimeLogsForTest(context.Background(), api.NewClient(server.URL, "token"), "dev-api-workspace", tc.once, tc.noLogs, nil)
			defer stop()
			select {
			case <-requested:
				t.Fatal("disabled runtime follower opened a stream")
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}
