package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestWaitForApproval checks the --wait-for-approval prefix
// (ADR-20261104140058 §6): without it a gated park prints the review link and
// fails; with it the CLI waits for the approval and parks once more.
func TestWaitForApproval(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		env       string
		wantCode  int
		wantPosts int
		wantErr   []string
	}{
		{name: "without the flag", args: []string{"park", "demo"}, wantCode: 1, wantPosts: 1, wantErr: []string{"Review: https://gregale.dev/approve/ap-1", "--wait-for-approval"}},
		{name: "with the flag", args: []string{"--wait-for-approval", "park", "demo"}, wantCode: 0, wantPosts: 2, wantErr: []string{"Waiting for approval (delete)", "Review: https://gregale.dev/approve/ap-1"}},
		{name: "with the env", args: []string{"park", "demo"}, env: "1", wantCode: 0, wantPosts: 2, wantErr: []string{"Waiting for approval (delete)"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var mu sync.Mutex
			posts, polls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/park":
					posts++
					if polls > 0 {
						_ = json.NewEncoder(w).Encode(map[string]any{})
						return
					}
					p := api.NewProblem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required", "a person must approve this request")
					p.AgentApproval = &api.AgentApprovalPendingInfo{ID: "ap-1", Category: api.AgentApprovalDelete, ReviewURL: "https://gregale.dev/approve/ap-1", ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(p.Status)
					_ = json.NewEncoder(w).Encode(p)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/agent-approvals/ap-1":
					polls++
					_ = json.NewEncoder(w).Encode(api.AgentApprovalResponse{ID: "ap-1", Status: api.AgentApprovalApproved})
				default:
					_ = json.NewEncoder(w).Encode(map[string]any{})
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			t.Setenv("GREGALE_WAIT_FOR_APPROVAL", tt.env)
			stderr := captureStderr(t)
			if code := main.Run(tt.args); code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			mu.Lock()
			defer mu.Unlock()
			if posts != tt.wantPosts {
				t.Fatalf("park posts = %d, want %d", posts, tt.wantPosts)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
		})
	}
}
