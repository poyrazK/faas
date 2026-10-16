package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowInitialRecoveryLifecycle(t *testing.T) {
	for _, scenario := range []string{"admitted", "changed", "evidence_removed", "evidence_expired", "wrong_evidence"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				run, app := seedResumeRun(t, s, simpleResumeSpec())
				failResumeRun(t, s, run, "send")
				inbox := s.(WorkflowInitialRecoveryStore)
				jobs := s.(EventRecoveryStore)
				page, err := inbox.ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, api.WorkflowInitialFailureOptions{})
				if err != nil || len(page.Items) != 1 || page.Items[0].RunID != run.ID || page.Coverage != "partial" {
					t.Fatalf("inbox %+v %v", page, err)
				}
				e := page.Items[0]
				preview, err := inbox.PreviewWorkflowInitialRecovery(ctx, app.AccountID, app.ID, e.ID)
				if err != nil || preview.Reason != "eligible" || preview.Target == nil || preview.Target.FailureEvidenceID != e.ID || preview.Target.ExpectedResumeCount != 0 {
					t.Fatalf("preview %+v %v", preview, err)
				}
				// Preview is independent of policy and does not resume the workflow.
				current, err := s.GetWorkflowRun(ctx, run.ID)
				if err != nil || current.ResumeCount != 0 {
					t.Fatalf("preview mutated run %+v %v", current, err)
				}
				req := api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: run.WorkflowName, Runs: []api.WorkflowRunRecoveryTarget{*preview.Target}}}
				if scenario == "wrong_evidence" {
					req.WorkflowRuns.Runs[0].FailureEvidenceID = uuid.NewString()
					if _, err = jobs.CreateEventRecovery(ctx, app.AccountID, app.ID, req); !errors.Is(err, ErrEventRecoveryChanged) {
						t.Fatalf("accepted unrelated evidence %v", err)
					}
					return
				}
				job, err := jobs.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil {
					t.Fatal(err)
				}
				if job.Selection.WorkflowRuns.Runs[0].FailureEvidenceID != e.ID {
					t.Fatal("job lost evidence identity")
				}
				if scenario == "changed" {
					if _, _, _, err = s.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "evidence_removed" {
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.workflowFailureEvidence, canonicalMemUUID(app.ID))
						m.mu.Unlock()
					} else {
						if _, err = s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_recovery_failure_evidence WHERE id=$1::uuid", e.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "evidence_expired" {
					aged := time.Now().UTC().Add(-api.WorkflowRecoveryFailureRetention - time.Hour)
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						entries := m.workflowFailureEvidence[canonicalMemUUID(app.ID)]
						entries[0].ObservedAt = aged
						entries[0].CompletedAt = nil
						m.mu.Unlock()
					} else if _, err = s.(*PgStore).pool.Exec(ctx, "UPDATE workflow_recovery_failure_evidence SET observed_at=$2,completed_at=NULL WHERE id=$1::uuid", e.ID, aged); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "evidence_removed" || scenario == "evidence_expired" {
					preflight, err := s.(EventRecoveryPreflightStore).GetEventRecoveryPreflight(ctx, app.AccountID, job.ID, time.Now().UTC())
					if err != nil || preflight.LikelySkippedCount != 1 || preflight.Sample[0].Reason != "failure_evidence_unavailable" {
						t.Fatalf("evidence preflight %+v %v", preflight, err)
					}
				}
				again, err := jobs.CreateEventRecovery(ctx, app.AccountID, app.ID, req)
				if err != nil || again.ID != job.ID {
					t.Fatalf("idempotency %+v %v", again, err)
				}
				did, err := jobs.ProcessNextEventRecovery(ctx, time.Now().UTC())
				if err != nil || !did {
					t.Fatalf("admission %t %v", did, err)
				}
				items, err := jobs.ListEventRecoveryItems(ctx, app.AccountID, job.ID, 0, 25)
				if err != nil || len(items.Items) != 1 {
					t.Fatalf("items %+v %v", items, err)
				}
				want := "queued"
				if scenario != "admitted" {
					want = "skipped"
				}
				if items.Items[0].State != want {
					t.Fatalf("item %+v want %s", items.Items[0], want)
				}
				if scenario == "admitted" {
					current, err = s.GetWorkflowRun(ctx, run.ID)
					if err != nil || current.ResumeCount != 1 {
						t.Fatalf("first recovery %+v %v", current, err)
					}
					changed, err := inbox.PreviewWorkflowInitialRecovery(ctx, app.AccountID, app.ID, e.ID)
					if err != nil || changed.Reason != "changed" || changed.Target != nil {
						t.Fatalf("stale preview %+v %v", changed, err)
					}
				}
				if _, err = inbox.PreviewWorkflowInitialRecovery(ctx, uuid.NewString(), app.ID, e.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("cross-account preview %v", err)
				}
			})
		})
	}
}
func TestWorkflowInitialRecoverySafety(t *testing.T) {
	for _, scenario := range []string{"capacity", "unsafe", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, s Store) {
				ctx := context.Background()
				run, app := seedResumeRun(t, s, simpleResumeSpec())
				failResumeRun(t, s, run, "send")
				inbox := s.(WorkflowInitialRecoveryStore)
				page, err := inbox.ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, api.WorkflowInitialFailureOptions{})
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "capacity":
					for n := 0; n < api.PlanHobby.WorkflowMaxConcurrentRuns(); n++ {
						if err = s.CreateWorkflowRun(ctx, &WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, DefinitionSnapshot: run.DefinitionSnapshot, Input: run.Input}); err != nil {
							t.Fatal(err)
						}
					}
				case "unsafe":
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						a := m.apps[app.ID]
						a.MaintenanceMode = true
						m.apps[app.ID] = a
						m.mu.Unlock()
					} else {
						if _, err = s.(*PgStore).pool.Exec(ctx, "UPDATE apps SET maintenance_mode=true WHERE id=$1::uuid", app.ID); err != nil {
							t.Fatal(err)
						}
					}
				case "missing":
					if m, ok := s.(*MemStore); ok {
						m.mu.Lock()
						delete(m.workflowRuns, run.ID)
						m.mu.Unlock()
					} else {
						if _, err = s.(*PgStore).pool.Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1::uuid", run.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				out, err := inbox.PreviewWorkflowInitialRecovery(ctx, app.AccountID, app.ID, page.Items[0].ID)
				want := map[string]string{"capacity": "capacity", "unsafe": "resume_ineligible", "missing": "run_not_retained"}[scenario]
				if err != nil || out.Reason != want || out.Target != nil {
					t.Fatalf("blocked preview %+v %v", out, err)
				}
			})
		})
	}
}
func TestWorkflowInitialFailurePagination(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, s, simpleResumeSpec())
		failResumeRun(t, s, run, "send")
		for n := 0; n < 2; n++ {
			other := &WorkflowRun{AppID: app.ID, WorkflowName: run.WorkflowName, DefinitionSnapshot: run.DefinitionSnapshot, Input: run.Input}
			if err := s.CreateWorkflowRun(ctx, other); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimNextDueWorkflowRun(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkWorkflowRunStatus(ctx, other.ID, WorkflowRunStatusDead, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		inbox := s.(WorkflowInitialRecoveryStore)
		o := api.WorkflowInitialFailureOptions{Limit: 1, Workflow: run.WorkflowName}
		seen := map[string]bool{}
		for n := 0; n < 3; n++ {
			page, err := inbox.ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, o)
			if err != nil || len(page.Items) != 1 || seen[page.Items[0].ID] {
				t.Fatalf("page %+v %v", page, err)
			}
			seen[page.Items[0].ID] = true
			if n == 0 {
				wrong := o
				wrong.Workflow = "other"
				wrong.After = page.NextAfter
				if _, err = inbox.ListWorkflowInitialFailures(ctx, app.AccountID, app.ID, wrong); !errors.Is(err, ErrEventRecoveryQuery) {
					t.Fatalf("cross-filter cursor %v", err)
				}
			}
			o.After = page.NextAfter
			if n < 2 && o.After == "" || n == 2 && o.After != "" {
				t.Fatal("incorrect pagination")
			}
		}
		if _, err := inbox.ListWorkflowInitialFailures(ctx, uuid.NewString(), app.ID, api.WorkflowInitialFailureOptions{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-account inbox %v", err)
		}
	})
}
