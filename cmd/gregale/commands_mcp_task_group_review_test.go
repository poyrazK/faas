package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskGroupReviewExportsPrivateTemplateAndPreservesConflict(t *testing.T) {
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	const prompt = "private review prompt"
	revision := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Error("group review did not use the current token")
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
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-input-review":{}}}}}`,
				req.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "gregale/tasks/input-review":
			if req.Params.TaskID == "unavailable-task" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","task":{"resultType":"complete","taskId":%q,"status":"input_required","createdAt":"2026-11-26T08:00:00Z","lastUpdatedAt":"2026-11-26T08:00:01Z","ttlMs":null},"reviewRevision":%q,"inputRequests":{"approval":{"method":"elicitation/create","params":{"mode":"form","message":%q,"requestedSchema":{"type":"object","properties":{}}}}}}}`,
				req.ID, req.Params.TaskID, revision, prompt)
		default:
			t.Errorf("unexpected group review method %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	group, output := t.TempDir(), t.TempDir()
	endpoint := server.URL + "/mcp"
	writeMCPTaskGroupTestFile(t, group, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":"%x","members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abc","taskId":"paused-task"},{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abd","taskId":"conflict-task"},{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abe","taskId":"unavailable-task"}]}`,
		revision, sha256.Sum256([]byte(endpoint))))
	target := filepath.Join(t.TempDir(), "untouched.json")
	if err := os.WriteFile(target, []byte("saved answers"), 0600); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(output, "member-1-input.json")
	if err := os.Symlink(target, conflict); err != nil {
		t.Fatal(err)
	}

	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"mcp", "client", "group", "review", "--url", endpoint, "--token-env", "GREGALE_TEST_GROUP_TOKEN", "--group", group, "--out-dir", output})
	restore()
	var entries []struct{ Outcome, TaskID, TemplatePath string }
	if err := json.Unmarshal([]byte(out.String()), &entries); err != nil || code != 0 || len(entries) != 3 ||
		entries[0].Outcome != "written" || entries[0].TaskID != "paused-task" ||
		entries[0].TemplatePath != filepath.Join(output, "member-0-input.json") ||
		entries[1].Outcome != "template_conflict" || entries[1].TemplatePath != conflict ||
		entries[2].Outcome != "unavailable" || entries[2].TaskID != "" || entries[2].TemplatePath != "" {
		t.Fatalf("group review exit=%d output=%s", code, out.String())
	}
	if strings.Contains(out.String(), prompt) || strings.Contains(out.String(), revision) {
		t.Fatalf("summary disclosed private template metadata: %s", out.String())
	}
	template, err := os.ReadFile(entries[0].TemplatePath)
	if err != nil || !strings.Contains(string(template), prompt) || !strings.Contains(string(template), revision) {
		t.Fatalf("review template missing private form or revision: %v", err)
	}
	if info, err := os.Stat(entries[0].TemplatePath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("review template must be private: info=%v err=%v", info, err)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "saved answers" {
		t.Fatalf("conflicting file was overwritten: %v", err)
	}
}
