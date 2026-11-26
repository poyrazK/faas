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
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskGroupStopReconcilesUncertainCancellation(t *testing.T) {
	dir := t.TempDir()
	groupID := strings.Repeat("a", 64)
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	var reads, cancels, lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marker, err := os.ReadFile(filepath.Join(dir, "stopped"))
		if err != nil || string(marker) != groupID {
			t.Error("request arrived before the stop marker was recorded")
		}
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Error("request did not use the current credential")
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				TaskID        string `json:"taskId"`
				SubmissionKey string `json:"submissionKey"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-submission-lookup":{}}}}}`, req.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "tasks/get":
			if req.Params.TaskID != "owned-task" {
				t.Error("read a Task outside the receipt")
			}
			fields := `"status":"working"`
			if reads.Add(1) == 2 {
				fields = `"status":"completed","result":{"private":"payload"}`
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","taskId":"owned-task","createdAt":"2026-11-26T08:00:00Z","lastUpdatedAt":"2026-11-26T08:00:01Z","ttlMs":null,%s}}`, req.ID, fields)
		case "tasks/cancel":
			cancels.Add(1)
			if req.Params.TaskID != "owned-task" {
				t.Error("cancelled a Task outside the receipt")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case "gregale/tasks/lookup":
			lookups.Add(1)
			if req.Params.SubmissionKey != "019ab2ce-217c-7abc-89ab-123456789ab1" {
				t.Error("looked up a key outside the receipt")
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	endpoint := server.URL + "/mcp"
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":"%x","members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789ab0","taskId":"owned-task"},{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789ab1"}]}`,
		groupID, sha256.Sum256([]byte(endpoint))))

	out, restore := main.CaptureStdout(t)
	exit := main.Run([]string{"mcp", "client", "group", "stop", "--url", endpoint, "--group", dir,
		"--token-env", "GREGALE_TEST_GROUP_TOKEN"})
	restore()
	var entries []map[string]any
	want := []map[string]any{
		{"receiptIndex": float64(0), "outcome": "terminal", "task": map[string]any{
			"taskId": "owned-task", "status": "completed", "lastUpdatedAt": "2026-11-26T08:00:01Z", "nextAction": "read_result",
		}},
		{"receiptIndex": float64(1), "outcome": "admission_unconfirmed"},
	}
	if err := json.Unmarshal([]byte(out.String()), &entries); err != nil || exit != 0 || !reflect.DeepEqual(entries, want) {
		t.Fatalf("group stop exit=%d output=%s decode=%v", exit, out.String(), err)
	}
	if reads.Load() != 2 || cancels.Load() != 1 || lookups.Load() != 1 {
		t.Fatalf("reads=%d cancellations=%d lookups=%d", reads.Load(), cancels.Load(), lookups.Load())
	}
}

func TestMCPTaskGroupStopRejectsConflictingMarkerBeforeRequests(t *testing.T) {
	dir := t.TempDir()
	groupID := strings.Repeat("a", 64)
	marker := strings.Repeat("b", 64)
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("conflicting marker must prevent requests")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	endpoint := server.URL + "/mcp"
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":"%x","members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789ab0","taskId":"owned-task"}]}`,
		groupID, sha256.Sum256([]byte(endpoint))))
	writeMCPTaskGroupTestFile(t, dir, "stopped", marker)
	stderr, restore := main.CaptureStderr(t)
	exit := main.Run([]string{"mcp", "client", "group", "stop", "--url", endpoint, "--group", dir,
		"--token-env", "GREGALE_TEST_GROUP_TOKEN"})
	restore()
	if exit == 0 || !strings.Contains(stderr.String(), "invalid MCP Task group stop marker") {
		t.Fatalf("conflicting marker exit=%d stderr=%s", exit, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "stopped"))
	if err != nil || string(got) != marker {
		t.Fatal("conflicting stop marker was changed")
	}
}
