package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Observe audit 2026-11-21 (finding 9): a busy app keeps request rows for
// only the first traced requests of each bucket-minute, and `gregale trace`
// gave no hint why the rest had no record.
func TestCmdTraceExplainsSampledOutTraces(t *testing.T) {
	traceID := "0af7651916cd43dd8448eb211c80319c"
	for _, tc := range []struct {
		name     string
		resp     api.AccountTraceLookupResponse
		wantNote bool
	}{
		{"nothing retained", api.AccountTraceLookupResponse{TraceID: traceID}, true},
		{"logs without a request row", api.AccountTraceLookupResponse{TraceID: traceID, Logs: []api.LogQueryEvent{
			{ID: "log-1", App: "alpha", Timestamp: "2026-11-21T10:00:00Z", Source: api.LogSourceHTTP, Method: "GET", Route: "/", Status: 200},
		}}, true},
		{"a request row without spans", api.AccountTraceLookupResponse{TraceID: traceID, Matches: []api.AccountTraceMatch{
			{App: "alpha", Request: api.DebugTelemetryRequestItem{ID: "row", Route: "/", Method: "GET", Status: 200}},
		}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/account/traces/"+traceID {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.resp)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test")
			stdout, _, restore := main.SwapIO(t)
			defer restore()
			oldJSON := *main.JsonOutput
			*main.JsonOutput = false
			defer func() { *main.JsonOutput = oldJSON }()
			// A trace with no evidence exits 1; the note is what matters here.
			_ = main.CmdTrace([]string{traceID})
			if got := strings.Contains(stdout.String(), "first 30 traced requests"); got != tc.wantNote {
				t.Fatalf("sampling note shown = %v, want %v:\n%s", got, tc.wantNote, stdout.String())
			}
		})
	}
}
