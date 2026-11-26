package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskGroupWatchPollsAfterSubscriptionRefusal(t *testing.T) {
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	const taskID = "saved-task-<>&-\u2028\u2029-\\u2028-\\u003c-雪"
	wireID, err := json.Marshal(taskID)
	if err != nil {
		t.Fatal(err)
	}
	const working = `"status":"working","lastUpdatedAt":"2026-11-26T08:00:01Z","pollIntervalMs":250`
	const paused = `"status":"input_required","lastUpdatedAt":"2026-11-26T08:00:02Z","inputRequests":{"form":{"method":"elicitation/create","params":{"message":"private-prompt","requestedSchema":{"type":"object"}}}}`
	var reads, subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Error("watch did not use the current token")
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
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{}}}}}`, req.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "subscriptions/listen":
			subscriptions.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "tasks/get":
			if req.Params.TaskID != taskID {
				t.Error("watch read a different Task")
			}
			fields := working
			switch reads.Add(1) {
			case 2:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			case 4:
				fields = paused
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","taskId":%s,"createdAt":"2026-11-26T08:00:00Z","ttlMs":null,%s}}`, req.ID, wireID, fields)
		default:
			t.Errorf("unexpected method %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	endpoint := server.URL + "/mcp"
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":"%x","members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abc","taskId":%s}]}`,
		strings.Repeat("a", 64), sha256.Sum256([]byte(endpoint)), wireID))
	out, restore := main.CaptureStdout(t)
	exit := main.Run([]string{"mcp", "client", "group", "watch", "--url", endpoint, "--group", dir,
		"--token-env", "GREGALE_TEST_GROUP_TOKEN", "--interval", "250ms", "--timeout", "3s"})
	restore()
	// SHA-256 vectors from Node JSON.stringify, including both real Unicode
	// separators and literal backslash escapes in the opaque Task ID.
	want := fmt.Sprintf(
		`{"receiptIndex":0,"outcome":"available","task":{"taskId":%s,"status":"working","lastUpdatedAt":"2026-11-26T08:00:01Z","pollIntervalMs":250,"nextAction":"poll_status"},"updateId":"325dbac26e15d4a8b8212ffae7a2b0d09c9f541369add3467635c8d934f41750"}`+"\n"+
			`{"receiptIndex":0,"outcome":"available","task":{"taskId":%s,"status":"input_required","lastUpdatedAt":"2026-11-26T08:00:02Z","nextAction":"provide_input"},"updateId":"ed27d314571b666218c2106955c3f6288f20c1570420dbb1027f04e8897be880"}`+"\n",
		wireID, wireID)

	if exit != 0 || out.String() != want {
		t.Fatalf("group watch exit=%d output=%s", exit, out.String())
	}
	if reads.Load() != 4 || subscriptions.Load() != 1 {
		t.Fatalf("group watch reads=%d subscriptions=%d", reads.Load(), subscriptions.Load())
	}
}
