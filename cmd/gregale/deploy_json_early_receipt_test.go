package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A JSON deploy that waits is silent until the release is live (292 s on
// 2026-11-05, #6634). The starter AGENTS.md points automation at --no-wait,
// whose receipt must carry the deployment id and a resume_command that waits
// for that same deployment without resubmitting it.
func TestJSONNoWaitReceiptCarriesResumeCommand(t *testing.T) {
	main.ResetJSONOut(t)
	submissions := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/apps" || r.URL.Path == "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/deployments":
			submissions++
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "accepted-deployment", AppID: "a1", Status: "pending"})
		case r.URL.Path == "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "acct", Plan: "scale"})
		default:
			http.Error(w, `{"status":404,"code":"not_found","title":"Not found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--non-interactive", "--json", "deploy", "--image", "registry.example/demo@sha256:" + strings.Repeat("a", 64), "--name", "demo", "--no-wait"})
	restore()
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr())
	}
	var receipt struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		SessionID     string `json:"session_id"`
		ResumeCommand string `json:"resume_command"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("stdout is not one JSON receipt: %v\n%s", err, stdout.String())
	}
	if receipt.ID != "accepted-deployment" || receipt.SessionID == "" || !strings.Contains(receipt.ResumeCommand, "release resume "+receipt.SessionID) {
		t.Fatalf("receipt = %+v", receipt)
	}
	if strings.Contains(receipt.ResumeCommand, " deploy") || submissions != 1 {
		t.Fatalf("resume_command %q would resubmit (submissions=%d)", receipt.ResumeCommand, submissions)
	}
}
