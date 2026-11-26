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
			if req.Params.TaskID != "saved-task" {
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
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","taskId":"saved-task","createdAt":"2026-11-26T08:00:00Z","ttlMs":null,%s}}`, req.ID, fields)
		default:
			t.Errorf("unexpected method %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	endpoint := server.URL + "/mcp"
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":"%x","members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abc","taskId":"saved-task"}]}`,
		strings.Repeat("a", 64), sha256.Sum256([]byte(endpoint))))
	out, restore := main.CaptureStdout(t)
	exit := main.Run([]string{"mcp", "client", "group", "watch", "--url", endpoint, "--group", dir,
		"--token-env", "GREGALE_TEST_GROUP_TOKEN", "--interval", "250ms", "--timeout", "3s"})
	restore()
	want := "" +
		`{"receiptIndex":0,"outcome":"available","task":{"taskId":"saved-task","status":"working","lastUpdatedAt":"2026-11-26T08:00:01Z","pollIntervalMs":250,"nextAction":"poll_status"}}` + "\n" +
		`{"receiptIndex":0,"outcome":"available","task":{"taskId":"saved-task","status":"input_required","lastUpdatedAt":"2026-11-26T08:00:02Z","nextAction":"provide_input"}}` + "\n"
	if exit != 0 || out.String() != want {
		t.Fatalf("group watch exit=%d output=%s", exit, out.String())
	}
	if reads.Load() != 4 || subscriptions.Load() != 1 {
		t.Fatalf("group watch reads=%d subscriptions=%d", reads.Load(), subscriptions.Load())
	}
}
