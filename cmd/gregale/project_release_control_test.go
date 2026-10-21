package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectDeployControlCLI(t *testing.T) {
	for _, action := range []string{"status", "abort", "recover"} {
		t.Run(action, func(t *testing.T) {
			attempt, request := strings.Repeat("c", 32), strings.Repeat("a", 32)
			posts := 0
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				safe := &api.ProjectSafeRelease{Status: "verifying", Message: "waiting for baseline health", Verification: &api.ProjectReleaseVerification{Progressive: true, Percent: 100, Generation: 7, Actions: []string{"abort"}}}
				if action == "recover" {
					safe.Status = "rollback_failed"
					safe.Verification.Actions = []string{"recover"}
				}
				if r.URL.Path != "/v1/project-submissions/"+attempt && r.URL.Path != "/v1/project-submissions/"+attempt+"/"+action {
					t.Errorf("unexpected route %s", r.URL.Path)
				}
				if r.Method == http.MethodPost {
					posts++
					var req api.ProjectReleaseControlRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					if req.RequestID != request || req.ExpectedGeneration != 7 {
						t.Errorf("lost control identity: %+v", req)
					}
					safe.Verification.Generation = 8
					safe.Verification.Control = &api.ProjectReleaseControl{Action: action, RequestID: req.RequestID, ExpectedGeneration: req.ExpectedGeneration, RequestedAt: time.Now(), Status: "pending"}
				}
				_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: safe}}})
			})
			args := []string{action, attempt}
			if action != "status" {
				args = append(args, "--request-id", request, "--generation", "7")
			}
			if code := cmdDeployTarball(args); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, osStderr)
			}
			wanted := 1
			if action == "status" {
				wanted = 0
			}
			if posts != wanted {
				t.Fatalf("writes=%d wanted=%d", posts, wanted)
			}
		})
	}
}

func TestProjectDeployControlRefusesMissingPolicy(t *testing.T) {
	posts := 0
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: strings.Repeat("c", 32)})
	})
	if code := cmdDeployTarball([]string{"abort", strings.Repeat("c", 32)}); code != 1 || posts != 0 {
		t.Fatalf("unsupported policy posted: exit=%d posts=%d", code, posts)
	}
}

func TestMigrationBaselineStatusBeforePublication(t *testing.T) {
	attempt := strings.Repeat("c", 32)
	posts := 0
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts++
		}
		safe := &api.ProjectSafeRelease{Status: "building", Verification: &api.ProjectReleaseVerification{}, ReleaseCommand: &api.ProjectReleaseCommand{Status: "succeeded", TaskID: "migration-task", VerifyBaseline: true, BaselineVerification: &api.ProjectMigrationBaselineVerification{Status: "unknown"}}}
		_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: safe}}})
	})
	if code := cmdDeployTarball([]string{"status", attempt}); code != 0 || posts != 0 {
		t.Fatalf("status exit=%d writes=%d", code, posts)
	}
}

func TestProjectDeployContractCLI(t *testing.T) {
	for _, scenario := range []string{"queued", "replay", "missing_ack", "not_planned"} {
		t.Run(scenario, func(t *testing.T) {
			attempt, request := strings.Repeat("c", 32), strings.Repeat("d", 32)
			posts := 0
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				safe := &api.ProjectSafeRelease{Status: "succeeded", Verification: &api.ProjectReleaseVerification{Progressive: true, Percent: 100, Generation: 9}, ReleaseCommand: &api.ProjectReleaseCommand{ContractCommandHash: strings.Repeat("f", 64), Contract: &api.ProjectMigrationContract{Status: "ready"}}}
				if scenario == "not_planned" {
					safe.ReleaseCommand = nil
				}
				if scenario == "replay" {
					safe.ReleaseCommand.Contract = &api.ProjectMigrationContract{Status: "uncertain", TaskID: "contract-task", RequestID: request, ExpectedGeneration: 7}
				}
				if r.Method == http.MethodPost {
					posts++
					if r.URL.Path != "/v1/project-submissions/"+attempt+"/contract" {
						t.Errorf("wrong action: %s", r.URL.Path)
					}
					var req api.ProjectReleaseControlRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					expected := uint64(9)
					if scenario == "replay" {
						expected = 7
					}
					if req.RequestID != request || req.ExpectedGeneration != expected {
						t.Errorf("lost contract identity: %+v", req)
					}
					safe.ReleaseCommand.Contract = &api.ProjectMigrationContract{Status: "queued", TaskID: "contract-task", RequestID: req.RequestID, ExpectedGeneration: req.ExpectedGeneration, RetiredDeployments: 2}
					if scenario == "missing_ack" {
						safe.ReleaseCommand.Contract.RequestID = "other"
					}
				}
				_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: safe}}})
			})
			args := []string{"contract", attempt}
			if scenario != "replay" {
				args = append(args, "--request-id", request)
			}
			want, writes := 0, 1
			if scenario == "missing_ack" {
				want = 1
			}
			if scenario == "not_planned" {
				want, writes = 1, 0
			}
			if code := cmdDeployTarball(args); code != want || posts != writes {
				t.Fatalf("exit=%d writes=%d want %d/%d", code, posts, want, writes)
			}
		})
	}
}
