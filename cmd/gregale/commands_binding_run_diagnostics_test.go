package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBindingRunDiagnosticsCLI(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	out := api.BindingVerificationRunDiagnostics{RunID: id, App: "api", DeploymentID: "22222222-2222-4222-8222-222222222222", Scope: "production", Status: "pending", CheckedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Blockers: []api.BindingVerificationDiagnosticBlocker{{Code: "policy_changed", Message: "Policy changed.", Recovery: "review_new_run"}}, Changes: []api.BindingVerificationDifference{{Field: "policy_revision", Reviewed: "1", Current: "2"}}, Recovery: []api.BindingVerificationRecoveryAction{{Action: "review_new_run", Message: "Review a new run."}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/"+id+"/diagnostics") {
			t.Errorf("unexpected diagnostic request: %s %s", r.Method, r.URL.Path)
		}
		if strings.HasPrefix(r.URL.Path, "/v1/apps/") {
			_ = json.NewEncoder(w).Encode(out)
		} else {
			_ = json.NewEncoder(w).Encode(api.ProjectBindingVerificationRunDiagnostics{RunID: id, Project: "project", Environment: "production", Status: out.Status, CheckedAt: out.CheckedAt, ExpiresAt: out.ExpiresAt, Complete: true, Members: map[string]api.BindingVerificationRunDiagnostics{"api": out}, Blockers: out.Blockers, Changes: out.Changes, Recovery: out.Recovery})
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"bindings", "verification-runs", "diagnostics", "api", id, "--json"}, {"projects", "environments", "release-sets", "verification-runs", "diagnostics", "project", "production", id, "--json"}} {
		code, stdout, stderr := captureBindingCLI(t, server.URL, args...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "policy_changed") || !strings.Contains(stdout, "review_new_run") || !strings.Contains(stdout, id) {
			t.Fatalf("missing structured diagnostics: %s %s", stdout, stderr)
		}
	}
}
