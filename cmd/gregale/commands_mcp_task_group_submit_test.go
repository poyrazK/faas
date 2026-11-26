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
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPTaskGroupSubmitKeepsRecordedKeyAfterUncertainReply(t *testing.T) {
	const fingerprint = "c53b021fd462b9015b17f7e86f3332fe923248cb8d04744b04946e9f4a2a2130"
	const plan = `[{"toolName":"build_report","arguments":{"report":"<>&\u2028-\\u2028","steps":2,"nested":{"\ue000":2.1,"😀":-0}},"deferFormInput":true}]`
	t.Setenv("GREGALE_TEST_GROUP_TOKEN", "current-token")
	dir := t.TempDir()
	planPath := filepath.Join(t.TempDir(), "plan.json")
	groupID, binding := strings.Repeat("a", 64), strings.Repeat("b", 64)
	stamp := fmt.Sprintf("%012x", time.Now().Add(-2*time.Minute).UnixMilli())
	planned := stamp[:8] + "-" + stamp[8:] + "-7abc-89ab-123456789abc"
	receiptPath := filepath.Join(dir, "member-0.json")
	var mu sync.Mutex
	var requests, verifies, submissions, lookups int
	var effectiveKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.Header.Get("Authorization") != "Bearer current-token" {
			t.Error("admission did not use the current token")
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				SubmissionKey string `json:"submissionKey"`
				GroupID       string `json:"groupId"`
				Binding       string `json:"binding"`
				ToolName      string `json:"toolName"`
				Name          string `json:"name"`
				Meta          struct {
					Key   string                    `json:"io.gregale/task-submission-key"`
					Owner mcphosting.TaskGroupOwner `json:"io.gregale/task-group-owner"`
					Caps  map[string]any            `json:"io.modelcontextprotocol/clientCapabilities"`
				} `json:"_meta"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method != r.Header.Get("Mcp-Method") {
			t.Error("HTTP and JSON-RPC method differ")
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "server/discover":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{},"io.gregale/task-submission":{},"io.gregale/task-submission-lookup":{},"io.gregale/task-group-owner":{}}}}}`,
				req.ID, mcphosting.ProtocolVersion, mcphosting.TasksExtensionID)
		case "gregale/tasks/group-owner":
			verifies++
			if req.Params.GroupID != groupID || req.Params.Binding != binding || req.Params.ToolName != "build_report" {
				t.Error("owner preflight did not bind the saved group and tool")
			}
			if _, err := os.Lstat(receiptPath); !os.IsNotExist(err) {
				t.Error("attempt recorded before verifying authorization")
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","binding":%q}}`, req.ID, binding)
		case "gregale/tasks/submit-bound":
			submissions++
			effectiveKey = req.Params.Meta.Key
			if effectiveKey == "" || effectiveKey == planned ||
				req.Params.Meta.Owner.GroupID != groupID || req.Params.Meta.Owner.Binding != binding ||
				req.Params.Name != "build_report" || req.Params.Meta.Caps["elicitation"] == nil {
				t.Error("bound admission did not use the refreshed key and saved request")
			}
			var receipt struct {
				Version              int    `json:"version"`
				SubmissionKey        string `json:"submissionKey"`
				PlannedSubmissionKey string `json:"plannedSubmissionKey"`
				RequestSHA256        string `json:"requestSha256"`
			}
			body, err := os.ReadFile(receiptPath)
			if err != nil || json.Unmarshal(body, &receipt) != nil || receipt.Version != 3 ||
				receipt.SubmissionKey != effectiveKey || receipt.PlannedSubmissionKey != planned || receipt.RequestSHA256 != fingerprint {
				t.Error("admission left before the refreshed receipt was recorded")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case "gregale/tasks/lookup":
			lookups++
			if effectiveKey == "" || req.Params.SubmissionKey != effectiveKey {
				t.Error("recovery did not use the effective receipt key")
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected method %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	endpoint := server.URL + "/mcp"
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(
		`{"version":3,"id":%q,"endpointSha256":"%x","ownerBinding":%q,"members":[{"submissionKey":%q,"requestSha256":%q}]}`,
		groupID, sha256.Sum256([]byte(endpoint)), binding, planned, fingerprint))
	writeMCPTaskGroupTestFile(t, filepath.Dir(planPath), filepath.Base(planPath), plan)
	args := []string{"mcp", "client", "group", "submit", "--url", endpoint, "--token-env", "GREGALE_TEST_GROUP_TOKEN",
		"--group", dir, "--plan", planPath}
	var saved string
	for range 2 {
		out, restore := main.CaptureStdout(t)
		code := main.Run(args)
		restore()
		var result []struct{ Outcome string }
		if err := json.Unmarshal([]byte(out.String()), &result); err != nil || code != 0 ||
			len(result) != 1 || result[0].Outcome != "unconfirmed" {
			t.Fatalf("submit exit=%d output=%s", code, out.String())
		}
		body, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatal(err)
		}
		if saved != "" && string(body) != saved {
			t.Fatal("recovery replaced the saved receipt")
		}
		saved = string(body)
	}
	mu.Lock()
	if verifies != 1 || submissions != 1 || lookups != 1 {
		t.Errorf("verifications=%d submissions=%d lookups=%d", verifies, submissions, lookups)
	}
	before := requests
	mu.Unlock()

	writeMCPTaskGroupTestFile(t, filepath.Dir(planPath), filepath.Base(planPath), strings.Replace(plan, `"steps":2`, `"steps":3`, 1))
	stderr, restore := main.CaptureStderr(t)
	code := main.Run(args)
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "does not match the saved request") {
		t.Fatalf("changed plan exit=%d stderr=%s", code, stderr.String())
	}
	writeMCPTaskGroupTestFile(t, filepath.Dir(planPath), filepath.Base(planPath), plan)
	writeMCPTaskGroupTestFile(t, dir, "stopped", groupID)
	stderr, restore = main.CaptureStderr(t)
	code = main.Run(args)
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "stop marker") {
		t.Fatalf("stopped group exit=%d stderr=%s", code, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != before {
		t.Fatal("changed or stopped plan contacted the endpoint")
	}
}
