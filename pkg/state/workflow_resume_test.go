package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func seedResumeRun(t *testing.T, store Store, spec api.WorkflowSpec) (*WorkflowRun, App) {
	t.Helper()
	ctx := context.Background()
	app, _ := seedWorkflowSchedule(t, store, "allow")
	snapshot, _ := json.Marshal(spec)
	run := &WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{"enabled":true}`), DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	steps := make([]*WorkflowStep, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		steps = append(steps, &WorkflowStep{StepName: step.Name, Input: run.Input})
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, steps); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	return run, app
}
func failResumeRun(t *testing.T, store Store, run *WorkflowRun, name string) {
	t.Helper()
	ctx := context.Background()
	step := guardStep(t, store, run.ID, name)
	if _, err := store.StartWorkflowStep(ctx, run.ID, name, step.Attempt+1, json.RawMessage(`{"literal":"{{input.keep_literal}}"}`)); err != nil {
		t.Fatal(err)
	}
	message := "provider unavailable"
	code := 503
	if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, name, WorkflowStepStatusDead, step.Attempt+1, &code, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
}
func resumeOptions(run *WorkflowRun, app App, count int) WorkflowResumeOptions {
	return WorkflowResumeOptions{RunID: run.ID, AppID: app.ID, AccountID: app.AccountID, ExpectedResumeCount: count}
}
func simpleResumeSpec() api.WorkflowSpec {
	return api.WorkflowSpec{Name: "recover", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send", Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}}}
}

func TestWorkflowResumePreservesProgressAndFencesOldWorkers(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		old := WithWorkflowRunGeneration(ctx, "", 0)
		spec := api.WorkflowSpec{Name: "resume", Steps: []api.WorkflowStepSpec{
			{Name: "done", Run: "done"},
			{Name: "send", Run: "send", DependsOn: []string{"done"}, When: &api.WorkflowGuardSpec{Ref: "input.enabled", Op: "eq", Value: json.RawMessage("true")}},
			{Name: "child", Run: "child", DependsOn: []string{"send"}},
			{Name: "off", Run: "off", When: &api.WorkflowGuardSpec{Ref: "input.enabled", Op: "eq", Value: json.RawMessage("false")}},
			{Name: "off-child", Run: "off-child", DependsOn: []string{"off"}},
		}}
		run, app := seedResumeRun(t, store, spec)
		old = WithWorkflowRunGeneration(old, run.ID, 0)
		if _, err := store.StartWorkflowStep(ctx, run.ID, "done", 1, run.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "done", WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`{"paid":true}`), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil {
			t.Fatal(err)
		}
		decision := guardStep(t, store, run.ID, "send").WhenEvaluatedAt
		if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "off"); err != nil {
			t.Fatal(err)
		}
		if err := store.SkipWorkflowStep(ctx, run.ID, "off-child", WorkflowSkipDependencySkipped); err != nil {
			t.Fatal(err)
		}
		// A dependency skip from the failed path is reversible.
		if err := store.SkipWorkflowStep(ctx, run.ID, "child", WorkflowSkipDependencyFailed); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		resumable := store.(WorkflowResumeStore)
		resumed, record, _, err := resumable.ResumeWorkflowRun(ctx, resumeOptions(run, app, 0))
		if err != nil || resumed.ResumeCount != 1 || resumed.Status != WorkflowRunStatusPending || resumed.FinishedAt != nil || resumed.LastError != nil {
			t.Fatalf("resume: %+v %v", resumed, err)
		}
		if !equalWorkflowJSON(resumed.DefinitionSnapshot, run.DefinitionSnapshot) || !equalWorkflowJSON(resumed.Input, run.Input) || record.PreviousStatus != WorkflowRunStatusDead || record.PreviousError == nil {
			t.Fatal("snapshot or previous failure lost")
		}
		if done := guardStep(t, store, run.ID, "done"); done.Attempt != 1 || !equalWorkflowJSON(done.Output, json.RawMessage(`{"paid":true}`)) {
			t.Fatal("completed action reset", done)
		}
		step := guardStep(t, store, run.ID, "send")
		if step.Attempt != 1 || step.RetryBase != 1 || !step.WhenEvaluatedAt.Equal(*decision) || !equalWorkflowJSON(step.Input, json.RawMessage(`{"literal":"{{input.keep_literal}}"}`)) {
			t.Fatalf("input, attempt or guard changed: %+v", step)
		}
		if guardStep(t, store, run.ID, "child").Status != WorkflowStepStatusPending || guardStep(t, store, run.ID, "off-child").Status != WorkflowStepStatusSkipped {
			t.Fatal("wrong descendants reopened")
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 2, step.Input); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("unclaimed resume dispatched: %v", err)
		}
		if err := store.MarkWorkflowRunStatus(old, run.ID, WorkflowRunStatusDead, nil, nil); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("old generation overwrote run: %v", err)
		}
		if err := store.RecoverWorkflowRun(old, run.ID); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("old worker recovered new run: %v", err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		current := WithWorkflowRunGeneration(ctx, run.ID, 1)
		if _, err := store.StartWorkflowStep(current, run.ID, "send", 2, step.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(old, run.ID, "send", WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`"stale"`), nil); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("stale result: %v", err)
		}
		if err := store.ScheduleWorkflowStepRetryWithHTTPStatus(ctx, run.ID, "send", 1, time.Now(), nil, "stale"); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("stale retry: %v", err)
		}
		// Interrupted resumed app calls consume their number, preserving history.
		if err := store.RecoverWorkflowRun(current, run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(current, run.ID, "send", 3, step.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(current, run.ID, "send", WorkflowStepStatusSucceeded, 3, nil, json.RawMessage(`"ok"`), nil); err != nil {
			t.Fatal(err)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != 3 || attempts[0].Status != WorkflowAttemptStatusFailed || attempts[1].Status != WorkflowAttemptStatusFailed || attempts[2].Status != WorkflowAttemptStatusSucceeded {
			t.Fatalf("history: %+v %v", attempts, err)
		}
		records, err := resumable.ListWorkflowResumes(ctx, run.ID)
		if err != nil || len(records) != 1 || records[0].AccountID != app.AccountID {
			t.Fatal("resume history missing", err)
		}
		records[0].ResumedSteps[0] = "modified"
		records[0].PreviousError = nil
		again, _ := resumable.ListWorkflowResumes(ctx, run.ID)
		if again[0].ResumedSteps[0] == "modified" || again[0].PreviousError == nil {
			t.Fatal("mutable history escaped")
		}
	})
}

func TestWorkflowResumeConcurrentRequestsAndQuota(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		run, app := seedResumeRun(t, store, simpleResumeSpec())
		failResumeRun(t, store, run, "send")
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0))
				if err == nil {
					wins.Add(1)
				} else if !errors.Is(err, ErrWorkflowResumeConflict) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("concurrent resume winners=%d", wins.Load())
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		account, err := store.AccountByID(ctx, app.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		for range account.Plan.WorkflowMaxConcurrentRuns() {
			pending := &WorkflowRun{AppID: app.ID, WorkflowName: "other", Input: json.RawMessage(`{}`), DefinitionSnapshot: run.DefinitionSnapshot}
			if err := store.CreateWorkflowRun(ctx, pending); err != nil {
				t.Fatal(err)
			}
		}
		_, _, active, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 1))
		if !errors.Is(err, ErrWorkflowRunQuotaExceeded) || active != account.Plan.WorkflowMaxConcurrentRuns() {
			t.Fatalf("quota bypass: active=%d err=%v", active, err)
		}
		unchanged, _ := store.GetWorkflowRun(ctx, run.ID)
		history, _ := store.(WorkflowResumeStore).ListWorkflowResumes(ctx, run.ID)
		if unchanged.ResumeCount != 1 || unchanged.Status != WorkflowRunStatusDead || len(history) != 1 {
			t.Fatal("rejected resume changed state")
		}
	})
}

func TestWorkflowResumePreservesAndFencesPlatformTenantIdentity(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		app, _ := seedWorkflowSchedule(t, store, "allow")
		required := true
		if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := json.Marshal(simpleResumeSpec())
		if err != nil {
			t.Fatal(err)
		}
		unbound := &WorkflowRun{AppID: app.ID, WorkflowName: "recover", DefinitionSnapshot: snapshot}
		if err := store.CreateWorkflowRun(ctx, unbound); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, unbound.ID, []*WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, unbound, "send")
		if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(unbound, app, 0)); !errors.Is(err, ErrWorkflowResumeUnavailable) {
			t.Fatalf("unbound run on tenant-required app resume error=%v, want unavailable", err)
		}
		tenants, ok := store.(PlatformTenantStore)
		if !ok {
			t.Fatal("workflow store does not implement platform tenant storage")
		}
		tenant, _, err := tenants.CreatePlatformTenant(ctx, app.AccountID, "resume-customer", "Resume customer", 10)
		if err != nil {
			t.Fatal(err)
		}
		run := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenant.ID, WorkflowName: "recover", Input: json.RawMessage(`{"invoice":42}`), DefinitionSnapshot: snapshot}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "send", Input: run.Input}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")

		options := resumeOptions(run, app, 0)
		options.PlatformTenantID = uuid.NewString()
		if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, options); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("foreign tenant resume error=%v, want not found", err)
		}
		options.PlatformTenantID = tenant.ID
		resumed, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, options)
		if err != nil || resumed.Status != WorkflowRunStatusPending || resumed.PlatformTenantID != tenant.ID {
			t.Fatalf("resumed tenant run=%+v err=%v", resumed, err)
		}
	})
}

func TestWorkflowResumeReplayRestrictions(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, kind := range []string{"cancelled", "unsafe-outbound", "compensation", "active-wait", "pre-dispatch-failure", "wrong-owner", "unavailable"} {
			t.Run(kind, func(t *testing.T) {
				ctx := context.Background()
				spec := simpleResumeSpec()
				switch kind {
				case "unsafe-outbound":
					spec.Steps[0].Run = ""
					spec.Steps[0].Retry = nil
					spec.Steps[0].Outbound = &api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/send"}
				case "compensation":
					spec.Steps[0].OnFailure = "undo"
					spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: "undo", Run: "undo"})
				case "active-wait":
					spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: "wait", WaitForEvent: "ready", Timeout: time.Hour})
				}
				run, app := seedResumeRun(t, store, spec)
				if kind == "active-wait" {
					if _, _, err := store.ParkWorkflowEvent(ctx, run.ID, "wait", "ready", time.Hour); err != nil {
						t.Fatal(err)
					}
					if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, run.Input); err != nil {
						t.Fatal(err)
					}
					message := "failed"
					if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", WorkflowStepStatusDead, 1, nil, nil, &message); err != nil {
						t.Fatal(err)
					}
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusDead, nil, &message); err != nil {
						t.Fatal(err)
					}
				} else if kind == "cancelled" {
					if _, err := store.CancelWorkflowRun(ctx, run.ID, "custom cancellation reason"); err != nil {
						t.Fatal(err)
					}
				} else if kind == "pre-dispatch-failure" {
					message := "bad mapping"
					if err := store.MarkWorkflowStepStatus(ctx, run.ID, "send", WorkflowStepStatusFailed, 0, nil, &message); err != nil {
						t.Fatal(err)
					}
					if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusFailed, nil, &message); err != nil {
						t.Fatal(err)
					}
				} else {
					if kind == "compensation" {
						if _, err := store.StartWorkflowStep(ctx, run.ID, "undo", 1, run.Input); err != nil {
							t.Fatal(err)
						}
						if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "undo", WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`{}`), nil); err != nil {
							t.Fatal(err)
						}
					}
					failResumeRun(t, store, run, "send")
				}
				opts := resumeOptions(run, app, 0)
				want := ErrWorkflowResumeUnsafe
				if kind == "wrong-owner" {
					opts.AccountID = uuid.NewString()
					want = ErrWorkflowRunNotFound
				}
				if kind == "unavailable" {
					value := true
					if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{MaintenanceMode: &value, SetMaintenanceMode: true}); err != nil {
						t.Fatal(err)
					}
					want = ErrWorkflowResumeUnavailable
				}
				if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, opts); !errors.Is(err, want) {
					t.Fatalf("%s: %v", kind, err)
				}
			})
		}
	})
}

func TestWorkflowResumeIntegrationBindingsAndLimit(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		integration := uuid.NewString()
		spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: integration, Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}}}}
		run, app := seedResumeRun(t, store, spec)
		failResumeRun(t, store, run, "send")
		resumer := store.(WorkflowResumeStore)
		if _, _, _, err := resumer.ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); !errors.Is(err, ErrWorkflowResumeUnavailable) {
			t.Fatalf("unbound integration resumed: %v", err)
		}
		bindings := store.(OutboundBindingStore)
		offer := OutboundIntegrationOffer{ID: integration, AccountID: app.AccountID, Name: "resume-crm", Origin: "https://api.example.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, CredentialSource: "customer_sealed", OwnerKind: "customer", RequestPolicy: api.DefaultOutboundRequestPolicy()}
		if _, err := bindings.CreateOutboundIntegration(ctx, offer); err != nil {
			t.Fatal(err)
		}
		if err := bindings.SetOutboundCredential(ctx, app.AccountID, integration, []byte("sealed-placeholder")); err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.BindOutboundIntegration(ctx, app.AccountID, app.ID, integration); err != nil {
			t.Fatal(err)
		}
		for count := range api.WorkflowRunMaxResumes {
			if _, _, _, err := resumer.ResumeWorkflowRun(ctx, resumeOptions(run, app, count)); err != nil {
				t.Fatalf("resume %d: %v", count, err)
			}
			if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
				t.Fatal(err)
			}
			failResumeRun(t, store, run, "send")
		}
		if _, _, _, err := resumer.ResumeWorkflowRun(ctx, resumeOptions(run, app, api.WorkflowRunMaxResumes)); !errors.Is(err, ErrWorkflowResumeLimit) {
			t.Fatalf("unbounded resumes: %v", err)
		}
		history, err := resumer.ListWorkflowResumes(ctx, run.ID)
		if err != nil || len(history) != api.WorkflowRunMaxResumes {
			t.Fatalf("history=%+v %v", history, err)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != api.WorkflowRunMaxResumes+1 {
			t.Fatalf("history overwritten: %+v %v", attempts, err)
		}
	})
}

func TestWorkflowResumeMigrationRollbackAndReapply(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	ctx := context.Background()
	run, app := seedResumeRun(t, store, simpleResumeSpec())
	failResumeRun(t, store, run, "send")
	if _, _, _, err := store.ResumeWorkflowRun(ctx, resumeOptions(run, app, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	failResumeRun(t, store, run, "send")
	migration, err := os.ReadFile("../../migrations/20261003180000001_workflow_resume.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(migration), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	if _, err := store.pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var historyExists bool
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM workflow_step_attempts WHERE run_id=$1", run.ID).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("downgrade destroyed attempts: %d %v", attempts, err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT to_regclass('workflow_run_resumes') IS NOT NULL").Scan(&historyExists); err != nil || historyExists {
		t.Fatalf("history table remains: %v %v", historyExists, err)
	}
	if _, err := store.pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	restored, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || restored.ResumeCount != 0 || restored.Status != WorkflowRunStatusDead || !equalWorkflowJSON(restored.DefinitionSnapshot, run.DefinitionSnapshot) {
		t.Fatalf("restore changed run: %+v %v", restored, err)
	}
	history, err := store.ListWorkflowResumes(ctx, run.ID)
	if err != nil || len(history) != 0 {
		t.Fatal("new history table is not empty", err)
	}
}
