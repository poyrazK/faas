package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPCLIHTTPErrorStatusAndExit(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		for _, command := range []string{"tools", "call", "lock", "legacy-initialize"} {
			t.Run(fmt.Sprintf("%s/%d", command, status), func(t *testing.T) {
				resetJSONOut(t)
				var requests atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("operator credential reached MCP endpoint")
					}
					w.Header().Set("Retry-After", "7")
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, `{"status":400,"code":"invalid_request","detail":"unsafe-upstream-marker"}`)
				}))
				defer s.Close()
				out, restoreOut := captureStdout(t)
				defer restoreOut()
				errOut, restoreErr := captureStderr(t)
				verb := command
				if command == "legacy-initialize" {
					verb = "tools"
				}
				args := []string{"mcp", verb, "--url", s.URL + "/mcp", "--json"}
				if command == "legacy-initialize" {
					args = append(args, "--legacy")
				}
				if command == "call" {
					args = append(args, "--tool", "charge")
				}
				if command == "lock" {
					args = append(args, "--out", filepath.Join(t.TempDir(), "contract.json"))
				}
				code := run(args)
				restoreErr()
				var problem api.Problem
				if err := json.Unmarshal([]byte(errOut.String()), &problem); err != nil {
					t.Fatalf("not one JSON Problem: %v: %s", err, errOut.String())
				}
				if problem.Status != status || code != exitCodeForStatus(status) || requests.Load() != 1 {
					t.Fatalf("status=%d exit=%d requests=%d: %s", problem.Status, code, requests.Load(), errOut.String())
				}
				if problem.RetryAfterSeconds == nil || *problem.RetryAfterSeconds != 7 || out.String() != "" || strings.Contains(errOut.String(), "unsafe-upstream-marker") {
					t.Fatalf("unsafe/missing error receipt: stdout=%s stderr=%s", out.String(), errOut.String())
				}
			})
		}
	}
}

func TestMCPCLIHTTPToolCallFailureIsNotRetried(t *testing.T) {
	resetJSONOut(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Method == "tools/list" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"charge","inputSchema":{"type":"object"}}]}}`, request.ID)
			return
		}
		if request.Method != "tools/call" {
			t.Errorf("unexpected MCP method: %s", request.Method)
		}
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	errOut, restoreErr := captureStderr(t)
	code := run([]string{"mcp", "call", "--url", s.URL + "/mcp", "--tool", "charge", "--json"})
	restoreErr()
	var problem api.Problem
	if err := json.Unmarshal([]byte(errOut.String()), &problem); err != nil {
		t.Fatal(err)
	}
	if code != 3 || problem.Status != 503 || calls.Load() != 1 {
		t.Fatalf("exit=%d calls=%d: %s", code, calls.Load(), errOut.String())
	}
}
