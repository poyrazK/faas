package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskGroupRespondAppliesOnlyAuthorizedUniqueSelections(t *testing.T) {
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	const prompt = "private approval prompt"
	const answer = "private answer"
	const schema = `{"properties":{"note":{"type":"string"}},"type":"object"}`
	revision := strings.Repeat("a", 64)
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Error("group response did not use the current token")
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				TaskID    string                              `json:"taskId"`
				Revision  string                              `json:"reviewRevision"`
				Responses map[string]mcphosting.InputResponse `json:"responses"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-input-review":{}}}}}`,
				req.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "gregale/tasks/input-review":
			reads.Add(1)
			switch req.Params.TaskID {
			case "unavailable-task":
				w.WriteHeader(http.StatusNotFound)
			case "chosen-task":
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","task":{"resultType":"complete","taskId":%q,"status":"input_required","createdAt":"2026-11-26T08:00:00Z","lastUpdatedAt":"2026-11-26T08:00:01Z","ttlMs":null},"reviewRevision":%q,"inputRequests":{"approval":{"method":"elicitation/create","params":{"mode":"form","message":%q,"requestedSchema":%s}}}}}`,
					req.ID, req.Params.TaskID, revision, prompt, schema)
			default:
				t.Errorf("looked up an ineligible selection %q", req.Params.TaskID)
				w.WriteHeader(http.StatusBadRequest)
			}
		case "gregale/tasks/respond-reviewed":
			writes.Add(1)
			response := req.Params.Responses["approval"]
			if req.Params.TaskID != "chosen-task" || req.Params.Revision != revision || len(req.Params.Responses) != 1 ||
				response.Action != "accept" || response.Content["note"] != answer {
				t.Error("response did not use the selected revision and answer")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","outcome":"applied","task":{"resultType":"complete","taskId":%q,"status":"working","createdAt":"2026-11-26T08:00:00Z","lastUpdatedAt":"2026-11-26T08:00:02Z","ttlMs":null}}}`,
				req.ID, req.Params.TaskID)
		default:
			t.Errorf("unexpected group response method %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	group, reviews := t.TempDir(), t.TempDir()
	endpoint := server.URL + "/mcp"
	endpointSHA := sha256.Sum256([]byte(endpoint))
	writeMCPTaskGroupTestFile(t, group, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":"%x","members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abc","taskId":"unavailable-task"},{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abd","taskId":"duplicate-task"},{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abe","taskId":"chosen-task"}]}`,
		revision, endpointSHA))
	formSHA := sha256.Sum256(fmt.Appendf(nil, `{"message":%q,"schema":%s}`, prompt, schema))
	writeReview := func(name, taskID, response string) string {
		t.Helper()
		writeMCPTaskGroupTestFile(t, reviews, name, fmt.Sprintf(
			`{"version":2,"taskId":%q,"endpointSha256":"%x","reviewRevision":%q,"forms":{"approval":{"message":%q,"schema":%s,"formSha256":"%x","response":%s}}}`,
			taskID, endpointSHA, revision, prompt, schema, formSHA, response))
		return filepath.Join(reviews, name)
	}
	accepted := fmt.Sprintf(`{"action":"accept","content":{"note":%q}}`, answer)
	chosen := writeReview("renamed.json", "chosen-task", accepted)
	duplicate := writeReview("first-copy.json", "duplicate-task", accepted)
	other := writeReview("other-group.json", "other-task", accepted)
	unavailable := writeReview("unavailable.json", "unavailable-task", accepted)
	unanswered := writeReview("second-copy.json", "duplicate-task", "null")

	args := []string{"mcp", "client", "group", "respond", "--url", endpoint, "--token-env", "GREGALE_TEST_GROUP_TOKEN", "--group", group}
	for _, path := range []string{chosen, duplicate, other, unavailable, unanswered} {
		args = append(args, "--reviewed-input-file", path)
	}
	out, restore := main.CaptureStdout(t)
	code := main.Run(args)
	restore()
	type entry struct {
		ReceiptIndex *int
		Outcome      string
		ReviewFile   string
	}
	var entries []entry
	if err := json.Unmarshal([]byte(out.String()), &entries); err != nil || code != 1 || len(entries) != 5 {
		t.Fatalf("group respond exit=%d output=%s", code, out.String())
	}
	for i, expected := range []entry{
		{new(0), "unavailable", unavailable},
		{new(2), "applied", chosen},
		{nil, "duplicate_review", duplicate},
		{nil, "not_in_group", other},
		{nil, "duplicate_review", unanswered},
	} {
		got := entries[i]
		if got.Outcome != expected.Outcome || got.ReviewFile != expected.ReviewFile ||
			(got.ReceiptIndex == nil) != (expected.ReceiptIndex == nil) ||
			(got.ReceiptIndex != nil && *got.ReceiptIndex != *expected.ReceiptIndex) {
			t.Fatalf("unexpected entry at index %d: %s", i, out.String())
		}
	}
	if reads.Load() != 2 || writes.Load() != 1 {
		t.Fatalf("unexpected Task operations: %d reads, %d writes", reads.Load(), writes.Load())
	}
	if strings.Contains(out.String(), prompt) || strings.Contains(out.String(), answer) || strings.Contains(out.String(), revision) {
		t.Fatalf("summary disclosed review contents: %s", out.String())
	}
}
