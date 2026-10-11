// adr: 644
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func diagnosticSnapshot(t *testing.T, store Store, runID string) []byte {
	t.Helper()
	run, err := store.GetWorkflowRun(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := store.GetWorkflowSteps(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.(WorkflowResumeStore).ListWorkflowResumes(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := make(map[string][]*WorkflowStepAttempt)
	for _, step := range steps {
		rows, err := store.GetWorkflowStepAttempts(t.Context(), runID, step.StepName)
		if err != nil {
			t.Fatal(err)
		}
		attempts[step.StepName] = rows
	}
	encoded, err := json.Marshal([]any{run, steps, history, attempts})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestWorkflowDiagnosticsReadOnlyAndResumeAgreement(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		spec := api.WorkflowSpec{Name: "recover", Steps: []api.WorkflowStepSpec{
			{Name: "done", Run: "done"}, {Name: "send", Run: "send", DependsOn: []string{"done"}},
			{Name: "child", Run: "child", DependsOn: []string{"send"}},
		}}
		run, app := seedResumeRun(t, store, spec)
		if _, err := store.StartWorkflowStep(t.Context(), run.ID, "done", 1, run.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(t.Context(), run.ID, "done", WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`{"private":"retained-result"}`), nil); err != nil {
			t.Fatal(err)
		}
		if err := store.SkipWorkflowStep(t.Context(), run.ID, "child", WorkflowSkipDependencyFailed); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		before := diagnosticSnapshot(t, store, run.ID)
		opts := WorkflowDiagnosticsOptions{RunID: run.ID, AccountID: app.AccountID}
		var preview api.WorkflowRunDiagnosticsResponse
		for range 2 {
			var err error
			preview, err = store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts)
			if err != nil || !preview.Resume.Eligible || preview.Resume.ExpectedResumeCount != 0 || !preview.LegacyUnpinned {
				t.Fatalf("preview=%+v error=%v", preview, err)
			}
		}
		if !reflect.DeepEqual(preview.Resume.ReopenedSteps, []string{"child", "send"}) || !reflect.DeepEqual(preview.Resume.PreservedSteps, []string{"done"}) {
			t.Fatalf("incorrect continuation: %+v", preview.Resume)
		}
		if string(before) != string(diagnosticSnapshot(t, store, run.ID)) {
			t.Fatal("diagnostics mutated run, steps, attempts or resume history")
		}
		encoded, _ := json.Marshal(preview)
		for _, private := range []string{"retained-result", "provider unavailable", "keep_literal", `"input"`, `"output"`, `"last_error"`} {
			if strings.Contains(string(encoded), private) {
				t.Fatalf("private data in diagnostics: %s", encoded)
			}
		}
		_, record, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(t.Context(), resumeOptions(run, app, preview.Resume.ExpectedResumeCount))
		if err != nil || !reflect.DeepEqual(record.ResumedSteps, preview.Resume.ReopenedSteps) {
			t.Fatalf("preview disagreed with resume: %+v %v", record, err)
		}
		fresh, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts)
		if err != nil || fresh.Resume.Eligible || fresh.Resume.ExpectedResumeCount != 1 || fresh.Resume.Blockers[0].Code != "run_not_failed" {
			t.Fatalf("stale preview accepted: %+v %v", fresh, err)
		}
		if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(t.Context(), resumeOptions(run, app, 0)); !errors.Is(err, ErrWorkflowResumeConflict) {
			t.Fatalf("stale generation resumed: %v", err)
		}
		opts.AccountID = uuid.NewString()
		if _, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("foreign account read: %v", err)
		}
		opts.AccountID, opts.PlatformTenantID = app.AccountID, uuid.NewString()
		if _, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("foreign tenant read: %v", err)
		}
	})
}

func TestWorkflowDiagnosticsQueueReasons(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, reason := range []string{"ready", "scheduled", "parked_wait", "retry_backoff", "app_capacity", "tenant_capacity", "workflow_capacity", "stale", "running"} {
			t.Run(reason, func(t *testing.T) {
				app, _ := seedWorkflowSchedule(t, store, "allow")
				now := time.Now().UTC()
				at, limit, tenant := now.Add(-time.Minute), 0, ""
				if reason == "tenant_capacity" {
					tenant = createFairDispatchTenant(t, store, app)
				}
				if reason == "scheduled" || reason == "parked_wait" || reason == "retry_backoff" {
					at = now.Add(time.Hour)
				}
				if reason == "workflow_capacity" {
					limit = 1
				}
				run := queueHealthRun(t, store, app, "target", tenant, at, limit)
				want := reason
				switch reason {
				case "parked_wait":
					queueHealthRunning(t, store, run)
					if err := store.ScheduleWorkflowRun(t.Context(), run.ID, WorkflowRunStatusAwaitingEvent, at); err != nil {
						t.Fatal(err)
					}
				case "retry_backoff":
					queueHealthRunning(t, store, run)
					if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*WorkflowStep{{StepName: "work"}}); err != nil {
						t.Fatal(err)
					}
					if _, err := store.StartWorkflowStep(t.Context(), run.ID, "work", 1, json.RawMessage(`{}`)); err != nil {
						t.Fatal(err)
					}
					if err := store.ScheduleWorkflowStepRetry(t.Context(), run.ID, "work", 1, at, "private error"); err != nil {
						t.Fatal(err)
					}
				case "app_capacity":
					for range api.WorkflowDispatchMaxPerApp {
						queueHealthRunning(t, store, queueHealthRun(t, store, app, "other", "", now, 0))
					}
				case "tenant_capacity":
					queueHealthRunning(t, store, queueHealthRun(t, store, app, "other", tenant, now, 0))
				case "workflow_capacity":
					other := queueHealthRun(t, store, app, "target", "", now.Add(time.Hour), 1)
					queueHealthRunning(t, store, other)
					if err := store.ScheduleWorkflowRun(t.Context(), other.ID, WorkflowRunStatusAwaitingEvent, now.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				case "stale", "running":
					queueHealthRunning(t, store, run)
					if reason == "stale" {
						queueHealthFixtureLease(t, store, run, now.Add(-time.Minute))
						want = "ready"
					}
				}
				result, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), WorkflowDiagnosticsOptions{RunID: run.ID, AccountID: app.AccountID})
				if err != nil || result.StateReason != want || result.StaleLease != (reason == "stale") {
					t.Fatalf("diagnostics=%+v %v", result, err)
				}
				if (reason == "scheduled" || reason == "parked_wait" || reason == "retry_backoff") && (result.NextWakeAt == nil || result.DueAgeSeconds != 0) {
					t.Fatalf("future wait diagnosed as due: %+v", result)
				}
				if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

func TestWorkflowDiagnosticsAdmissionBlockers(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, code := range []string{"maintenance", "tenant_required", "active_run_quota"} {
			t.Run(code, func(t *testing.T) {
				run, app := seedResumeRun(t, store, simpleResumeSpec())
				failResumeRun(t, store, run, "send")
				yes := true
				switch code {
				case "maintenance":
					if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{MaintenanceMode: &yes, SetMaintenanceMode: true}); err != nil {
						t.Fatal(err)
					}
				case "tenant_required":
					if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{PlatformTenantRequired: &yes, SetPlatformTenantRequired: true}); err != nil {
						t.Fatal(err)
					}
				case "active_run_quota":
					for range api.PlanHobby.WorkflowMaxConcurrentRuns() {
						queueHealthRun(t, store, app, "other", "", time.Now().Add(time.Hour), 0)
					}
				}
				result, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), WorkflowDiagnosticsOptions{RunID: run.ID, AccountID: app.AccountID})
				if err != nil || result.Resume.Eligible || len(result.Resume.Blockers) != 1 || result.Resume.Blockers[0].Code != code || !reflect.DeepEqual(result.Resume.ReopenedSteps, []string{"send"}) {
					t.Fatalf("preview=%+v %v", result, err)
				}
				_, _, _, err = store.(WorkflowResumeStore).ResumeWorkflowRun(t.Context(), resumeOptions(run, app, 0))
				if err == nil {
					t.Fatal("resume ignored preview blocker")
				}
			})
		}
	})
}

func TestWorkflowResumePreciseBlockReasons(t *testing.T) {
	for _, code := range []string{"cancelled", "resume_limit_reached", "invalid_definition", "incomplete_step_state", "active_step", "handler_executed", "failed_control_step", "failure_before_dispatch", "unsafe_mutation", "no_failed_actions"} {
		t.Run(code, func(t *testing.T) {
			spec := simpleResumeSpec()
			run := WorkflowRun{Status: WorkflowRunStatusDead}
			steps := map[string]WorkflowStep{"send": {Status: WorkflowStepStatusDead, Attempt: 1}}
			switch code {
			case "cancelled":
				now := time.Now()
				run.CancelledAt = &now
			case "resume_limit_reached":
				run.ResumeCount = api.WorkflowRunMaxResumes
			case "incomplete_step_state":
				delete(steps, "send")
			case "active_step":
				steps["send"] = WorkflowStep{Status: WorkflowStepStatusAwaitingEvent}
			case "handler_executed":
				spec.Steps[0].OnFailure = "undo"
				spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: "undo", Run: "undo"})
				steps["undo"] = WorkflowStep{Status: WorkflowStepStatusSucceeded, Attempt: 1}
			case "failed_control_step":
				spec.Steps[0] = api.WorkflowStepSpec{Name: "send", WaitForDuration: time.Minute}
			case "failure_before_dispatch":
				steps["send"] = WorkflowStep{Status: WorkflowStepStatusDead}
			case "unsafe_mutation":
				spec.Steps[0] = api.WorkflowStepSpec{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/send"}}
			case "no_failed_actions":
				steps["send"] = WorkflowStep{Status: WorkflowStepStatusSucceeded, Attempt: 1}
			}
			run.DefinitionSnapshot, _ = json.Marshal(spec)
			if code == "invalid_definition" {
				run.DefinitionSnapshot = json.RawMessage(`{`)
			}
			_, _, err := workflowResumePlan(run, steps, run.ResumeCount, api.PlanHobby)
			if err == nil || workflowDiagnosticBlock(err).Code != code {
				t.Fatalf("expected %s, got %v", code, err)
			}
		})
	}
}

func TestWorkflowDiagnosticsPinnedCodeAndConcurrentAdmission(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, dep := seedWorkflowSchedule(t, store, "allow")
		snapshot, _ := json.Marshal(simpleResumeSpec())
		run := &WorkflowRun{AppID: app.ID, DeploymentID: dep.ID, WorkflowName: "recover", DefinitionSnapshot: snapshot}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(t.Context()); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		replaceWorkflowDeployment(t, store, app)
		opts := WorkflowDiagnosticsOptions{RunID: run.ID, AccountID: app.AccountID}
		preview, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts)
		if err != nil || !preview.Resume.Eligible || preview.LegacyUnpinned || preview.DeploymentID != dep.ID {
			t.Fatalf("lost original code: %+v %v", preview, err)
		}
		for range api.PlanHobby.WorkflowMaxConcurrentRuns() {
			queueHealthRun(t, store, app, "other", "", time.Now().Add(time.Hour), 0)
		}
		before := diagnosticSnapshot(t, store, run.ID)
		if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(t.Context(), resumeOptions(run, app, preview.Resume.ExpectedResumeCount)); !errors.Is(err, ErrWorkflowRunQuotaExceeded) {
			t.Fatalf("preview reserved quota: %v", err)
		}
		if string(before) != string(diagnosticSnapshot(t, store, run.ID)) {
			t.Fatal("rejected continuation changed pinned run")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(ctx, opts); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled observation=%v", err)
		}
	})
}

func TestWorkflowDiagnosticsTenantLinkRevocationBlocksResume(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		tenantID := createFairDispatchTenant(t, store, app)
		consumer, err := store.CreateAPIConsumer(t.Context(), app.AccountID, app.ID, "diagnostic-customer", "Customer")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.(PlatformTenantStore).LinkPlatformTenantConsumer(t.Context(), app.AccountID, tenantID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		snapshot, _ := json.Marshal(simpleResumeSpec())
		run := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenantID, WorkflowName: "recover", DefinitionSnapshot: snapshot}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(t.Context()); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		opts := WorkflowDiagnosticsOptions{RunID: run.ID, AccountID: app.AccountID, PlatformTenantID: tenantID}
		before, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts)
		if err != nil || !before.Resume.Eligible {
			t.Fatalf("linked preview=%+v %v", before, err)
		}
		if _, err := store.RevokeAPIConsumer(t.Context(), app.AccountID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		after, err := store.(WorkflowRunDiagnosticsStore).GetWorkflowRunDiagnostics(t.Context(), opts)
		if err != nil || after.Resume.Eligible || after.Resume.Blockers[0].Code != "tenant_unavailable" {
			t.Fatalf("revoked link=%+v %v", after, err)
		}
		if _, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(t.Context(), resumeOptions(run, app, before.Resume.ExpectedResumeCount)); !errors.Is(err, ErrWorkflowResumeUnavailable) {
			t.Fatalf("revoked tenant resumed: %v", err)
		}
	})
}

func TestWorkflowDiagnosticsStepKindsIncludePersistedBatchItems(t *testing.T) {
	spec := api.WorkflowSpec{Name: "kinds", Steps: []api.WorkflowStepSpec{
		{Name: "action", Run: "act"},
		{Name: "timer", WaitForDuration: time.Minute},
		{Name: "event", WaitForEvent: "finished", Timeout: time.Hour},
		{Name: "callback", WaitForCallback: true, Timeout: time.Hour},
		{Name: "condition", WaitForCondition: &api.WorkflowConditionSpec{Run: "check", Interval: time.Minute, MaxAttempts: 2}},
		{Name: "join", Join: &api.WorkflowJoinSpec{}},
		{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "item"}}},
	}}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string]WorkflowStep{}
	for _, s := range spec.Steps {
		steps[s.Name] = WorkflowStep{StepName: s.Name}
	}
	parent := "batch"
	for index := range api.WorkflowForEachMaxItems {
		name := api.WorkflowForEachItemName(parent, index)
		steps[name] = WorkflowStep{StepName: name, ForEachParent: &parent}
	}
	kinds := workflowDiagnosticStepKinds(WorkflowRun{DefinitionSnapshot: snapshot}, steps)
	for _, name := range []string{"action", "timer", "event", "callback", "condition", "join"} {
		if kinds[name] != name {
			t.Fatalf("%s diagnosed as %s", name, kinds[name])
		}
	}
	if kinds["batch"] != "for_each" || kinds[api.WorkflowForEachItemName(parent, api.WorkflowForEachMaxItems-1)] != "action" {
		t.Fatalf("batch kinds=%v", kinds)
	}
}
