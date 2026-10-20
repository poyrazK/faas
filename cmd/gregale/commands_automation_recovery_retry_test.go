package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomationRecoveryRetryReviewedSelection(t *testing.T) {
	preview := main.AutomationRecoveryRetryPreview{App: "consumer", AppID: inspectorAppID, Name: "paid", ParentJobID: recoveryInvocationID, Rows: []main.AutomationRecoveryRetryRow{{Position: 1, RunID: inspectorRunID, ExpectedResumeCount: 1, Eligible: true}, {Position: 2, RunID: recoverySecondRunID, ExpectedResumeCount: 2, Reason: "run_changed"}}}
	for _, positions := range [][]string{nil, {"2"}, {"3"}, {"1", "1"}} {
		if _, err := main.AutomationRecoveryRetryPlan(preview, positions, "retry-1"); err == nil {
			t.Fatalf("accepted %v", positions)
		}
	}
	p, err := main.AutomationRecoveryRetryPlan(preview, []string{"1"}, "retry-1")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "retry.json")
	if err := main.WriteAutomationRecoveryPlanFile(path, p); err != nil {
		t.Fatal(err)
	}
	saved, err := main.ReadAutomationRecoveryPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ParentJobID != preview.ParentJobID || len(saved.Runs) != 1 || saved.Runs[0].ExpectedResumeCount != 1 || saved.Runs[0].ParentPosition != 1 {
		t.Fatalf("plan=%+v", saved)
	}
	req := main.AutomationRunJobRequest(saved, 2, "")
	if req.ParentJobID != preview.ParentJobID || req.WorkflowRuns.Runs[0].ParentPosition != 1 || req.WorkflowRuns.Runs[0].ExpectedResumeCount != 1 {
		t.Fatalf("request=%+v", req)
	}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%v %v", info, err)
	}
	if err := main.WriteAutomationRecoveryPlanFile(path, p); err == nil {
		t.Fatal("overwrote reviewed plan")
	}
	p.CreatedAt = time.Now().UTC()
	main.CaptureAutomationStdout(t)
	journal := filepath.Join(t.TempDir(), "journal.jsonl")
	if code := main.CmdAutomations([]string{"recovery-plan", "--plan", path, "--journal", journal}); code == 0 {
		t.Fatal("retry plan executed without server proof")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("created local retry journal")
	}
}

func TestAutomationRecoveryRetryPreviewReadOnly(t *testing.T) {
	for _, scenario := range []string{"eligible", "waiting", "changed", "unknown", "succeeded", "blocked"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("preview mutated state: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				write := func(v any) {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
					}
				}
				switch r.URL.Path {
				case "/v1/apps/consumer":
					write(api.AppResponse{ID: inspectorAppID})
				case "/v1/event-recoveries/" + recoveryInvocationID + "/preflight":
					if r.URL.RawQuery != "retry=true" {
						t.Error("missing retry view")
					}
					resume := 1
					item := api.EventRecoveryPreflightItem{Position: 1, WorkflowRunID: inspectorRunID, ExpectedResumeCount: &resume, SavedOutcome: "failed", Status: "eligible", Reason: "eligible"}
					switch scenario {
					case "waiting":
						item.Status = "waiting"
						item.Reason = "capacity"
						item.CapacityScope = "app"
						item.Blockers = []string{"active_run_quota"}
					case "changed":
						item.Status = "likely_skipped"
						item.Reason = "changed"
					case "unknown":
						item.Status = "unknown"
						item.Reason = "outcome_not_saved"
						item.SavedOutcome = ""
					case "succeeded":
						item.Status = "likely_skipped"
						item.Reason = "outcome_not_failed"
						item.SavedOutcome = "succeeded"
					case "blocked":
						item.Status = "likely_skipped"
						item.Reason = "resume_ineligible"
					}
					write(api.EventRecoveryPreflight{JobID: recoveryInvocationID, AppID: inspectorAppID, WorkflowName: "paid", ReviewMode: "workflow_retry", ReviewedCount: 1, ObservedAt: now, Sample: []api.EventRecoveryPreflightItem{item}})
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			path := filepath.Join(t.TempDir(), "retry.json")
			out := main.CaptureAutomationStdout(t)
			code := main.CmdAutomations([]string{"recovery-retry-plan", "--app", "consumer", "--job", recoveryInvocationID, "--position", "1", "--key", "retry-1", "--plan-out", path})
			if (code == 0) != (scenario == "eligible" || scenario == "waiting") {
				t.Fatalf("exit=%d", code)
			}
			if strings.Contains(out.String(), "PRIVATE") {
				t.Fatal("leaked workflow payload")
			}
			if scenario != "eligible" && scenario != "waiting" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("saved ineligible plan")
				}
			}
		})
	}
}
