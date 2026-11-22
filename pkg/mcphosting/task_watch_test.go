package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchTaskBySubmissionKeyReconcilesWithoutNotifications(t *testing.T) {
	const key = "019ab2ce-217c-7abc-89ab-123456789abc"
	for _, quiet := range []bool{false, true} {
		name, expectedReads := "unsupported subscription", int32(3)
		if quiet {
			name, expectedReads = "silent subscription", 2
		}
		t.Run(name, func(t *testing.T) {
			var reads, subscriptions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				case "gregale/tasks/lookup":
					if request.Params.SubmissionKey != key || r.Header.Get("Authorization") != "Bearer caller-token" {
						t.Error("receipt lookup did not retain its key and credential")
					}
					status := "working"
					if reads.Add(1) >= expectedReads {
						status = "input_required"
					}
					writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"retained","status":%q,"createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, status, taskTimestamp, taskTimestamp))
				case "subscriptions/listen":
					subscriptions.Add(1)
					if !quiet {
						w.WriteHeader(http.StatusNotImplemented)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged",
						"params": map[string]any{"notifications": map[string]any{"taskIds": []string{"retained"}}}})
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				default:
					t.Errorf("unexpected MCP method %q", request.Method)
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL+"/mcp", "caller-token", ProtocolVersion)
			if err != nil {
				t.Fatal(err)
			}
			advertiseTasks(client)
			client.Capabilities.Extensions["io.gregale/task-submission-lookup"] = json.RawMessage("{}")
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			var statuses []string
			err = client.WatchTaskBySubmissionKey(ctx, key, 100*time.Millisecond, func(task Task) error {
				statuses = append(statuses, task.Status)
				return nil
			})
			if err != nil || !slices.Equal(statuses, []string{"working", "input_required"}) ||
				reads.Load() != expectedReads || subscriptions.Load() != 1 {
				t.Fatalf("statuses=%v lookups=%d subscriptions=%d error=%v", statuses, reads.Load(), subscriptions.Load(), err)
			}
		})
	}
}
