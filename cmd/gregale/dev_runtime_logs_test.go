package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
			if got := formatDevRuntimeLog(test.data); got != test.want {
				t.Fatalf("formatDevRuntimeLog(%q) = %q, want %q", test.data, got, test.want)
			}
		})
	}
}

func TestStreamDevRuntimeLogsRendersAppStream(t *testing.T) {
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

	oldOut := osStdout
	var output strings.Builder
	osStdout = &output
	defer func() { osStdout = oldOut }()

	if err := streamDevRuntimeLogs(context.Background(), api.NewClient(server.URL, "token"), "demo"); err != nil {
		t.Fatalf("streamDevRuntimeLogs: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "runtime stdout | ready\n") || !strings.Contains(got, "runtime stderr | warning\n") {
		t.Fatalf("runtime output = %q, want both structured lines", got)
	}
}
