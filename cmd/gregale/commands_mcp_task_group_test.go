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

func TestMCPTaskGroupStatusUsesRenewedReceiptKey(t *testing.T) {
	const planned = "019ab2ce-217c-7abc-89ab-123456789ab0"
	const renewed = "019ab2ce-217c-7abc-89ab-123456789ab1"
	const unsent = "019ab2ce-217c-7abc-89ab-123456789ab2"
	const invalid = "019ab2ce-217c-7abc-89ab-123456789ab3"
	const taskID = "recovered-task"
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	var lookups, reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Error("group read did not use the current token")
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				SubmissionKey string `json:"submissionKey"`
				TaskID        string `json:"taskId"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		task := fmt.Sprintf(`"resultType":"complete","taskId":%q,"status":"completed","createdAt":"2026-11-26T08:00:00Z","lastUpdatedAt":"2026-11-26T08:00:01Z","ttlMs":null`, taskID)
		switch request.Method {
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-submission-lookup":{}}}}}`, request.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "gregale/tasks/lookup":
			lookups.Add(1)
			if request.Params.SubmissionKey != renewed {
				t.Error("lookup must use the effective saved key only")
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{%s}}`, request.ID, task)
		case "tasks/get":
			reads.Add(1)
			if request.Params.TaskID != taskID {
				t.Error("read must use the recovered Task ID")
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{%s,"result":{"text":"retained"}}}`, request.ID, task)
		default:
			t.Errorf("unexpected request %q", request.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(server.URL+"/")))
	requestDigest, proof := strings.Repeat("a", 64), strings.Repeat("b", 64)
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(`{"version":3,"id":%q,"endpointSha256":%q,"ownerBinding":%q,"members":[{"submissionKey":%q,"requestSha256":%q},{"submissionKey":%q,"requestSha256":%q},{"submissionKey":%q,"requestSha256":%q}]}`,
		proof, digest, proof, planned, requestDigest, unsent, requestDigest, invalid, requestDigest))
	writeMCPTaskGroupTestFile(t, dir, "member-0.json", fmt.Sprintf(`{"version":3,"endpointSha256":%q,"submissionKey":%q,"plannedSubmissionKey":%q,"requestSha256":%q,"credentialProof":%q}`,
		digest, renewed, planned, requestDigest, proof))
	// A symlink to an otherwise valid private receipt is still invalid.
	if err := os.Symlink("member-0.json", filepath.Join(dir, "member-2.json")); err != nil {
		t.Fatal(err)
	}

	want := []map[string]any{
		{"receiptIndex": float64(0), "outcome": "available", "task": map[string]any{
			"taskId": taskID, "status": "completed", "lastUpdatedAt": "2026-11-26T08:00:01Z", "nextAction": "read_result",
		}},
		{"receiptIndex": float64(1), "outcome": "not_submitted"},
		{"receiptIndex": float64(2), "outcome": "invalid_receipt"},
	}
	args := []string{"mcp", "client", "group", "status", "--url", server.URL, "--group", dir,
		"--token-env", "GREGALE_TEST_GROUP_TOKEN"}
	for i, include := range []bool{false, true} {
		runArgs := append([]string(nil), args...)
		if include {
			runArgs = append(runArgs, "--include-results")
			want[0]["task"].(map[string]any)["result"] = map[string]any{"text": "retained"}
		}
		out, restore := main.CaptureStdout(t)
		exit := main.Run(runArgs)
		restore()
		var entries []map[string]any
		if err := json.Unmarshal([]byte(out.String()), &entries); err != nil || exit != 0 || !reflect.DeepEqual(entries, want) {
			t.Fatalf("group includeResults=%v exit=%d output=%s decode=%v", include, exit, out.String(), err)
		}
		if lookups.Load() != int32(i+1) || reads.Load() != int32(i) {
			t.Fatalf("group lookups=%d reads=%d after pass %d", lookups.Load(), reads.Load(), i)
		}
	}
}

func TestMCPTaskGroupStatusRejectsMismatchedEndpointBeforeRequests(t *testing.T) {
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("endpoint mismatch must be detected before making requests")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	dir := t.TempDir()
	digest := strings.Repeat("a", 64)
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(`{"version":1,"id":%q,"endpointSha256":%q,"members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abc"}]}`, digest, digest))
	stderr, restore := main.CaptureStderr(t)
	exit := main.Run([]string{"mcp", "client", "group", "status", "--url", server.URL, "--group", dir,
		"--token-env", "GREGALE_TEST_GROUP_TOKEN"})
	restore()
	if exit == 0 || !strings.Contains(stderr.String(), "does not match the selected endpoint") {
		t.Fatalf("group mismatch exit=%d stderr=%s", exit, stderr.String())
	}
}

func writeMCPTaskGroupTestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
