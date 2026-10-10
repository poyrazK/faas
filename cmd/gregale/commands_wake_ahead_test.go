package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func runWakeAheadCLI(t *testing.T, args ...string) (int, string, []string) {
	t.Helper()
	var requests []string
	enabled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/v1/apps/demo/service-wake-ahead" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPut {
			var req api.SetServiceWakeAheadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
				t.Errorf("bad PUT body: %v", err)
			} else {
				enabled = *req.Enabled
			}
		}
		writeJSONTest(w, api.ServiceWakeAheadResponse{Slug: "demo", Enabled: enabled})
	}))
	t.Cleanup(server.Close)
	resetJSONOut(t)
	setPreviewTestAuth(t)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	code := run(append([]string{"wake-ahead"}, args...))
	return code, output.String(), requests
}

func TestWakeAheadCLI(t *testing.T) {
	tests := []struct {
		args     []string
		request  string
		contains string
	}{
		{[]string{"demo"}, "GET /v1/apps/demo/service-wake-ahead", "is off for demo"},
		{[]string{"demo", "on"}, "PUT /v1/apps/demo/service-wake-ahead", "is on for demo"},
		{[]string{"demo", "off"}, "PUT /v1/apps/demo/service-wake-ahead", "is off for demo"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			code, out, requests := runWakeAheadCLI(t, tt.args...)
			if code != 0 || len(requests) != 1 || requests[0] != tt.request || !strings.Contains(out, tt.contains) {
				t.Fatalf("exit %d, requests %v, output %q", code, requests, out)
			}
		})
	}
}

func TestWakeAheadCLIRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"demo", "maybe"}, {"demo", "on", "extra"}, {"Bad Slug"}} {
		if code, _, requests := runWakeAheadCLI(t, args...); code == 0 || len(requests) != 0 {
			t.Fatalf("args %v: exit %d, requests %v", args, code, requests)
		}
	}
}
