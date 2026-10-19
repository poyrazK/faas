package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBindingAutomaticDiagnosticsCLI(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, plain := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "graph", false: "app"}[graph], map[bool]string{true: "plain", false: "json"}[plain]}, "/"), func(t *testing.T) {
				id := "11111111-1111-4111-8111-111111111111"
				expiry := time.Now().UTC().Add(time.Minute)
				diagnostic := api.BindingVerificationRunDiagnostics{RunID: id, App: "api", DeploymentID: "22222222-2222-4222-8222-222222222222", Scope: "production", Status: "pending", CheckedAt: time.Now().UTC(), ExpiresAt: expiry, Complete: false, Blockers: []api.BindingVerificationDiagnosticBlocker{{Code: "policy_changed", Message: "Policy changed.", Recovery: "review_new_run"}}, Changes: []api.BindingVerificationDifference{}, Recovery: []api.BindingVerificationRecoveryAction{{Action: "review_new_run", Message: "Review a new receipt."}}}
				reads, resumes := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resume"):
						resumes++
						w.WriteHeader(http.StatusConflict)
						_ = json.NewEncoder(w).Encode(api.NewProblem(http.StatusConflict, "binding_verification_run_changed", "Verification stopped", "Original reviewed selection changed."))
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/diagnostics"):
						reads++
						if graph {
							_ = json.NewEncoder(w).Encode(api.ProjectBindingVerificationRunDiagnostics{RunID: id, Project: "project", Environment: "production", Status: "pending", CheckedAt: diagnostic.CheckedAt, ExpiresAt: expiry, Complete: false, Members: map[string]api.BindingVerificationRunDiagnostics{}, Blockers: diagnostic.Blockers, Changes: diagnostic.Changes, Recovery: diagnostic.Recovery})
						} else {
							_ = json.NewEncoder(w).Encode(diagnostic)
						}
					default:
						t.Errorf("unexpected recovery request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				defer server.Close()
				args := []string{"bindings", "verification-runs", "resume", "api", id}
				if graph {
					args = []string{"projects", "environments", "release-sets", "verification-runs", "resume", "project", "production", id}
				}
				if !plain {
					args = append(args, "--json")
				}
				code, stdout, stderr := main.CaptureBindingCLI(t, server.URL, args...)
				if code != 1 || reads != 1 || resumes != 1 {
					t.Fatalf("failure routing: code=%d reads=%d resumes=%d %s %s", code, reads, resumes, stdout, stderr)
				}
				output := stdout
				if plain {
					output += stderr
				}
				if !strings.Contains(output, "policy_changed") || !strings.Contains(output, "review_new_run") || !strings.Contains(output, id) {
					t.Fatalf("missing recovery explanation: %s", output)
				}
				if !plain {
					var value map[string]any
					if err := json.Unmarshal([]byte(stdout), &value); err != nil || value["diagnostics"] == nil || value["passed"] != false {
						t.Fatalf("structured failure: %s %v", stdout, err)
					}
				}
			})
		}
	}
}
