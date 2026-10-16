package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeployMigrationRecoveryCLI(t *testing.T) {
	for _, scenario := range []string{"resolved", "replay", "missing_ack", "missing_attestation"} {
		t.Run(scenario, func(t *testing.T) {
			attempt, request, replacement := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
			task := "11111111-1111-4111-8111-111111111111"
			hash := strings.Repeat("d", 64)
			posts := 0
			migrationRecoveryCLI(t, func(w http.ResponseWriter, r *http.Request) {
				outcome := &api.ProjectMigrationContract{Status: "uncertain", TaskID: task, ExpectedGeneration: 9}
				now := time.Now().UTC()
				saved := &api.ProjectMigrationRecovery{Status: "resolved", RequestID: request, TaskID: task, ExpectedGeneration: 9, ReplacementAttemptID: replacement, ReplacementReleaseID: "22222222-2222-4222-8222-222222222222", EvidenceHash: hash, OperatorAccountID: "33333333-3333-4333-8333-333333333333", ResolvedAt: &now}
				if scenario == "replay" {
					outcome.Recovery = saved
				}
				if r.Method == http.MethodPost {
					posts++
					if r.URL.Path != "/v1/project-submissions/"+attempt+"/resolve-contract" {
						t.Error("wrong recovery action")
					}
					var req api.ProjectMigrationRecoveryRequest
					if json.NewDecoder(r.Body).Decode(&req) != nil || !req.Valid() || req.RequestID != request || req.ExpectedTaskID != task || req.ExpectedGeneration != 9 || req.ReplacementAttemptID != replacement {
						t.Error("recovery request identity changed")
					}
					if scenario != "missing_ack" {
						outcome.Recovery = saved
					}
				}
				safe := &api.ProjectSafeRelease{Status: "succeeded", ReleaseCommand: &api.ProjectReleaseCommand{Contract: outcome}}
				_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: safe}}})
			})
			args := []string{"resolve-contract", attempt, "--database-state-checked", "--execution-quiescent"}
			if scenario != "replay" {
				args = append(args, "--replacement-attempt", replacement, "--request-id", request, "--evidence-hash", hash)
			}
			if scenario == "missing_attestation" {
				args = []string{"resolve-contract", attempt, "--replacement-attempt", replacement, "--request-id", request, "--evidence-hash", hash}
			}
			status := cmdProjectMigrationRecovery(args[1:])
			if scenario == "resolved" || scenario == "replay" {
				if status != 0 || posts != 1 {
					t.Fatalf("status=%d posts=%d", status, posts)
				}
			} else if status == 0 || scenario == "missing_attestation" && posts != 0 {
				t.Fatalf("unsafe recovery accepted: status=%d posts=%d", status, posts)
			}
		})
	}
}

func migrationRecoveryCLI(t *testing.T, handler http.HandlerFunc) {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	jsonOutput = true
	osStdout, osStderr = &bytes.Buffer{}, &bytes.Buffer{}
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
}
