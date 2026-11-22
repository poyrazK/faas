package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskWaitReceiptLookupUnavailable(t *testing.T) {
	const tokenEnv = "GREGALE_TEST_MCP_TASK_TOKEN"
	const token = "receipt-client-token"
	const key = "019ab2ce-217c-7abc-89ab-123456789abc"
	t.Setenv(tokenEnv, token)
	var lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("request did not use the current caller credential")
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				SubmissionKey string `json:"submissionKey"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-submission-lookup":{}}}}}`, request.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "gregale/tasks/lookup":
			lookups.Add(1)
			if request.Params.SubmissionKey != key {
				t.Error("lookup used a different submission key")
			}
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected operation after an unresolved lookup: %s", request.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	endpoint := server.URL + "/mcp"
	receipt := filepath.Join(t.TempDir(), "task.json")
	body := fmt.Appendf(nil, `{"version":1,"endpointSha256":"%x","submissionKey":%q}`, sha256.Sum256([]byte(endpoint)), key)
	if err := os.WriteFile(receipt, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"mcp", "client", "wait", "--url", endpoint, "--task-receipt", receipt,
		"--token-env", tokenEnv, "--cancel-on-timeout", "--json"})
	restoreErr()
	restoreOut()
	if code != 3 || lookups.Load() != 1 {
		t.Fatalf("receipt wait exit=%d lookups=%d stderr=%s", code, lookups.Load(), stderr.String())
	}
	want := map[string]any{"status": "unknown", "pollIntervalMs": float64(7000), "nextAction": "retry_status"}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("receipt wait returned %s, want %v", out.String(), want)
	}
}

func TestMCPTaskMetadataStatus(t *testing.T) {
	const taskID = "saved-task"
	const stamp = "2026-11-21T12:00:00Z"
	const private = "private-task-payload-marker"
	type response struct {
		httpStatus int
		status     string
		fields     string
		want       string
		exit       int
	}
	working := response{status: "working", fields: `,"pollIntervalMs":0`,
		want: `{"taskId":"saved-task","status":"working","lastUpdatedAt":"2026-11-21T12:00:00Z","pollIntervalMs":250,"nextAction":"poll_status"}`}
	for _, tc := range []struct {
		name      string
		responses []response
	}{
		{"reconnect with the same task", []response{
			working,
			{httpStatus: http.StatusServiceUnavailable, exit: 3,
				want: `{"taskId":"saved-task","status":"unknown","pollIntervalMs":7000,"nextAction":"retry_status"}`},
			working,
			{status: "completed", fields: `,"result":{"secret":"` + private + `"}`,
				want: `{"taskId":"saved-task","status":"completed","lastUpdatedAt":"2026-11-21T12:00:00Z","nextAction":"read_result"}`},
		}},
		{"default polling delay", []response{
			{status: "working", want: `{"taskId":"saved-task","status":"working","lastUpdatedAt":"2026-11-21T12:00:00Z","pollIntervalMs":2000,"nextAction":"poll_status"}`},
		}},
		{"input required", []response{
			{status: "input_required", fields: `,"inputRequests":{"form":{"method":"elicitation/create","params":{"message":"` + private + `","requestedSchema":{"type":"object","properties":{}}}}}`,
				want: `{"taskId":"saved-task","status":"input_required","lastUpdatedAt":"2026-11-21T12:00:00Z","nextAction":"provide_input"}`},
		}},
		{"failed task", []response{
			{status: "failed", fields: `,"error":{"message":"` + private + `"}`,
				want: `{"taskId":"saved-task","status":"failed","lastUpdatedAt":"2026-11-21T12:00:00Z","nextAction":"none"}`},
		}},
		{"cancelled task", []response{
			{status: "cancelled", want: `{"taskId":"saved-task","status":"cancelled","lastUpdatedAt":"2026-11-21T12:00:00Z","nextAction":"none"}`},
		}},
		{"unauthenticated", []response{{httpStatus: http.StatusUnauthorized, exit: 2}}},
		{"forbidden", []response{{httpStatus: http.StatusForbidden, exit: 6}}},
		{"missing or inaccessible", []response{{httpStatus: http.StatusNotFound, exit: 4}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GREGALE_TEST_MCP_TASK_TOKEN", "task-client-token")
			var discoveries, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer task-client-token" {
					t.Error("status request changed the caller's credential")
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						TaskID string `json:"taskId"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				switch req.Method {
				case "server/discover":
					discoveries.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{}}}}}`, req.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
				case "tasks/get":
					if req.Params.TaskID != taskID || r.Header.Get("Mcp-Name") != taskID {
						t.Error("status read a different task")
					}
					index := int(reads.Add(1)) - 1
					if index >= len(tc.responses) {
						t.Error("status unexpectedly retried")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					reply := tc.responses[index]
					if reply.httpStatus != 0 {
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set("Retry-After", "7")
						w.Header().Set("WWW-Authenticate", private)
						w.WriteHeader(reply.httpStatus)
						_, _ = fmt.Fprintf(w, `{"detail":%q}`, private)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","taskId":%q,"status":%q,"createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"statusMessage":%q%s}}`,
						req.ID, taskID, reply.status, stamp, stamp, private, reply.fields)
				default:
					t.Errorf("status issued an unexpected operation %q", req.Method)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()

			for i, reply := range tc.responses {
				out, restoreOut := main.CaptureStdout(t)
				errOut, restoreErr := main.CaptureStderr(t)
				code := main.Run([]string{"mcp", "client", "status", "--url", server.URL + "/mcp",
					"--task-id", taskID, "--metadata-only", "--token-env", "GREGALE_TEST_MCP_TASK_TOKEN", "--json"})
				restoreErr()
				restoreOut()
				stdout, stderr := out.String(), errOut.String()
				if code != reply.exit || reads.Load() != int32(i+1) || discoveries.Load() != int32(i+1) {
					t.Fatalf("status read %d: exit=%d reads=%d discovers=%d: %s", i, code, reads.Load(), discoveries.Load(), stderr)
				}
				if strings.Contains(stdout+stderr, private) {
					t.Fatal("status exposed a raw task payload")
				}
				if reply.want == "" {
					var problem api.Problem
					if stdout != "" || json.Unmarshal([]byte(stderr), &problem) != nil || problem.Status != reply.httpStatus {
						t.Fatalf("inaccessible task must return an HTTP problem: stdout=%s stderr=%s", stdout, stderr)
					}
					continue
				}
				var got, want map[string]any
				if json.Unmarshal([]byte(stdout), &got) != nil || json.Unmarshal([]byte(reply.want), &want) != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("status read %d = %s, want %s: %s", i, stdout, reply.want, stderr)
				}
			}
		})
	}
}
