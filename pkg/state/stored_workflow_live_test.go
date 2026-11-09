// adr: 1291
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedLiveVerification(t *testing.T, store state.Store) (state.App, state.Deployment, state.StoredWorkflow, api.StoredWorkflowTestSuite) {
	t.Helper()
	app, dep := state.SeedWorkflowSchedule(t, store, "allow")
	author := store.(state.StoredWorkflowStore)
	draft := state.MutateForTest(t, author, app.ID, "live-cases", "save", 0, state.StoredWorkflowDraft("live-cases", "/handler", ""), false)
	draft = state.MutateForTest(t, author, app.ID, draft.Name, "publish", draft.Version, nil, false)
	count := 2
	suite := api.StoredWorkflowTestSuite{Cases: []api.StoredWorkflowTestCase{{Name: "real main", Simulation: api.StoredWorkflowTestInputs{
		Input: json.RawMessage(`{"id":9007199254740993,"private":"PRIVATE-INPUT"}`), MockOutputs: map[string]json.RawMessage{"main": json.RawMessage(`"PRIVATE-MOCK"`)}},
		Expectations: []api.StoredWorkflowCheckExpectation{{Step: "main", State: "mocked"}},
		Live:         &api.StoredWorkflowLiveTest{Expectations: []api.StoredWorkflowLiveExpectation{{Step: "main", State: "succeeded", Output: json.RawMessage(`null`), AttemptCount: &count}}}}}}
	draft = saveSuiteForTest(t, author, app.ID, draft, &suite)
	return app, dep, draft, suite
}

func liveStartRequest(draft state.StoredWorkflow, dep state.Deployment) api.StartStoredWorkflowLiveVerificationRequest {
	return api.StartStoredWorkflowLiveVerificationRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion,
		ExpectedDeploymentID: dep.ID, CaseName: "real main", ConfirmRealActions: true}
}

func finishLiveRun(t *testing.T, store state.Store, runID string, output json.RawMessage, attempt int) {
	t.Helper()
	if err := store.CreateWorkflowSteps(t.Context(), runID, []*state.WorkflowStep{{RunID: runID, StepName: "main", Status: state.WorkflowStepStatusPending}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowStepStatus(t.Context(), runID, "main", state.WorkflowStepStatusSucceeded, attempt, output, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(t.Context(), runID, state.WorkflowRunStatusSucceeded, json.RawMessage(`"PRIVATE-RUN-OUTPUT"`), nil); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationLiveVerificationProvenanceAndReports(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, dep, draft, suite := seedLiveVerification(t, store)
		live := store.(state.StoredWorkflowLiveStore)
		req, key := liveStartRequest(draft, dep), uuid.NewString()
		view, replayed, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, key, req, true)
		if err != nil || replayed || view.Outdated || view.Report != nil || view.RunStatus != "pending" {
			t.Fatalf("start: %+v %v", view, err)
		}
		run, err := store.GetWorkflowRun(t.Context(), view.RunID)
		if err != nil || run.DeploymentID != dep.ID || !strings.Contains(string(run.Input), "9007199254740993") || strings.Contains(string(run.Input), "PRIVATE-MOCK") || len(run.DefinitionSnapshot) == 0 {
			t.Fatal("live run did not pin authoritative input, definition and code", err)
		}
		pending, err := live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || pending.Report != nil || pending.ReportCurrent {
			t.Fatal("pending run acquired a verdict", err)
		}
		finishLiveRun(t, store, view.RunID, json.RawMessage(`null`), 2)
		checked, err := live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || checked.Report == nil || !checked.Report.Passed || !checked.ReportCurrent || checked.Outdated {
			t.Fatalf("check: %+v %v", checked, err)
		}
		encoded, _ := json.Marshal(checked)
		if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "expectations") || strings.Contains(string(encoded), "9007199254740993") {
			t.Fatal("verification response leaked payloads")
		}
		// Returned values cannot mutate retained observations in the memory store.
		checked.Report.Steps[0].Passed = false
		restored, err := live.GetStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || !restored.Report.Steps[0].Passed {
			t.Fatal("retained report aliased caller memory", err)
		}
		before, _ := store.(state.StoredWorkflowStore).ListStoredWorkflows(t.Context(), app.ID)
		if before[0].Version != draft.Version || !before[0].UpdatedAt.Truncate(time.Microsecond).Equal(draft.UpdatedAt.Truncate(time.Microsecond)) {
			t.Fatal("verification advanced stored workflow intent")
		}
		suite.Cases[0].Live.Expectations[0].Output = json.RawMessage(`"PRIVATE-NEW-EXPECTATION"`)
		draft = saveSuiteForTest(t, store.(state.StoredWorkflowStore), app.ID, draft, &suite)
		old, err := live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || !old.Outdated || !old.Report.Passed {
			t.Fatal("edited assertions replaced the frozen live test", err)
		}
		again, replayed, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, key, req, false)
		if err != nil || !replayed || again.RunID != view.RunID {
			t.Fatal("idempotent replay revalidated a changed target or runtime", err)
		}
		changed := req
		changed.ExpectedVersion = draft.Version
		if _, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, key, changed, true); !errors.Is(err, state.ErrWorkflowRunIdempotencyConflict) {
			t.Fatal("same key accepted a different request", err)
		}
		if _, err := live.GetStoredWorkflowLiveVerification(t.Context(), uuid.NewString(), draft.Name, view.RunID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-app report read succeeded", err)
		}
		if _, err := live.GetStoredWorkflowLiveVerification(t.Context(), app.ID, "other", view.RunID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-stored workflow report read succeeded", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := live.CheckStoredWorkflowLiveVerification(cancelled, app.ID, draft.Name, view.RunID); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled check persisted observations", err)
		}
		draft = saveSuiteForTest(t, store.(state.StoredWorkflowStore), app.ID, draft, nil)
		if _, err := live.GetStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("clear retained frozen live data", err)
		}
		if _, err := store.GetWorkflowRun(t.Context(), view.RunID); err != nil {
			t.Fatal("clear deleted the actual execution", err)
		}
	})
}

func TestStoredWorkflowLiveVerificationAdmissionFencesAndIdempotency(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, dep, draft, _ := seedLiveVerification(t, store)
		live := store.(state.StoredWorkflowLiveStore)
		req := liveStartRequest(draft, dep)
		for _, tc := range []struct {
			name string
			edit func(*api.StartStoredWorkflowLiveVerificationRequest)
		}{
			{"unconfirmed", func(r *api.StartStoredWorkflowLiveVerificationRequest) { r.ConfirmRealActions = false }},
			{"stale case", func(r *api.StartStoredWorkflowLiveVerificationRequest) { r.ExpectedVersion-- }},
			{"stale publication", func(r *api.StartStoredWorkflowLiveVerificationRequest) { r.ExpectedPublishedVersion-- }},
			{"stale deployment", func(r *api.StartStoredWorkflowLiveVerificationRequest) { r.ExpectedDeploymentID = uuid.NewString() }},
			{"unknown case", func(r *api.StartStoredWorkflowLiveVerificationRequest) { r.CaseName = "missing" }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				bad := req
				tc.edit(&bad)
				if _, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), bad, true); err == nil {
					t.Fatal("invalid admission succeeded")
				}
			})
		}
		if _, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), req, false); err == nil {
			t.Fatal("disabled runtime admitted a run")
		}
		runs, _, _ := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if len(runs) != 0 {
			t.Fatal("rejected starts left real runs")
		}
		key := uuid.NewString()
		var wg sync.WaitGroup
		ids, errs := make(chan string, 4), make(chan error, 4)
		for range 4 {
			wg.Go(func() {
				v, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, key, req, true)
				ids <- v.RunID
				errs <- err
			})
		}
		wg.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		first := ""
		for id := range ids {
			if first == "" {
				first = id
			}
			if id != first {
				t.Fatal("concurrent replay admitted multiple runs")
			}
		}
		runs, _, _ = store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if len(runs) != 1 {
			t.Fatal("concurrent requests consumed multiple run slots")
		}
		finishLiveRun(t, store, first, json.RawMessage(`"PRIVATE-ACTUAL"`), 1)
		v, err := live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, first)
		if err != nil || v.Report == nil || v.Report.Passed || !reflect.DeepEqual(v.Report.Steps[0].Mismatches, []string{"output", "attempt_count"}) {
			t.Fatal("safe failure fields missing", err)
		}
	})
}

func TestStoredWorkflowLiveVerificationChangedTargetsAndResume(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, dep, draft, _ := seedLiveVerification(t, store)
		live := store.(state.StoredWorkflowLiveStore)
		req := liveStartRequest(draft, dep)
		view, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), req, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(t.Context(), view.RunID, []*state.WorkflowStep{{StepName: "main"}}); err != nil {
			t.Fatal(err)
		}
		run, err := store.ClaimNextDueWorkflowRun(t.Context())
		if err != nil || run.ID != view.RunID {
			t.Fatal("claim live fixture", err)
		}
		state.FailResumeRun(t, store, run, "main")
		checked, err := live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || checked.Report == nil || checked.Report.Passed || !checked.ReportCurrent {
			t.Fatal("failure report", err)
		}
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(t.Context(), state.ResumeOptions(run, app, 0)); err != nil {
			t.Fatal(err)
		}
		resumed, err := live.GetStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || resumed.ReportCurrent || resumed.Report == nil {
			t.Fatal("resume left a current terminal report", err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx := state.WithWorkflowRunGeneration(t.Context(), view.RunID, 1)
		if _, err := store.StartWorkflowStep(ctx, view.RunID, "main", 2, run.Input); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepStatus(ctx, view.RunID, "main", state.WorkflowStepStatusSucceeded, 2, json.RawMessage(`null`), nil); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, view.RunID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		checked, err = live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || !checked.Report.Passed || !checked.ReportCurrent || checked.Report.ResumeCount != 1 {
			t.Fatal("resumed report", err)
		}
		draft = state.MutateForTest(t, store.(state.StoredWorkflowStore), app.ID, draft.Name, "publish", draft.Version, nil, false)
		changed, err := live.GetStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || !changed.Outdated {
			t.Fatal("new publication left current evidence", err)
		}
		newDep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:live-new", Workflows: json.RawMessage(`[]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), newDep.ID); err != nil {
			t.Fatal(err)
		}
		listing, err := live.ListStoredWorkflowLiveVerifications(t.Context(), app.ID, draft.Name)
		if err != nil || listing.DeploymentID != newDep.ID || !listing.Verifications[0].Outdated {
			t.Fatal("deployment freshness", err)
		}
		stale := liveStartRequest(draft, dep)
		if _, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), stale, true); !errors.Is(err, state.ErrStoredWorkflowVersionConflict) {
			t.Fatal("old deployment admitted", err)
		}
	})
}

func TestStoredWorkflowLiveVerificationUsesNormalRunQuotaAndPlan(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, dep, draft, _ := seedLiveVerification(t, store)
		live := store.(state.StoredWorkflowLiveStore)
		req := liveStartRequest(draft, dep)
		if err := store.UpdateAccountPlan(t.Context(), app.AccountID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		if _, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), req, true); err == nil {
			t.Fatal("plan downgrade admitted real execution")
		}
		if err := store.UpdateAccountPlan(t.Context(), app.AccountID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		for range api.PlanHobby.WorkflowMaxConcurrentRuns() {
			r := &state.WorkflowRun{AppID: app.ID, DeploymentID: dep.ID, WorkflowName: draft.Name, DefinitionSnapshot: draft.Published}
			if err := store.CreateWorkflowRun(t.Context(), r); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), req, true); err == nil {
			t.Fatal("live verification bypassed normal active-run quota")
		}
		listing, err := live.ListStoredWorkflowLiveVerifications(t.Context(), app.ID, draft.Name)
		if err != nil || len(listing.Verifications) != 0 {
			t.Fatal("quota rejection persisted verification data", err)
		}
	})
}

func TestStoredWorkflowLiveVerificationLoopTargetsAndCancellation(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, dep, draft, suite := seedLiveVerification(t, store)
		author := store.(state.StoredWorkflowStore)
		def := api.WorkflowSpec{Name: draft.Name, Steps: []api.WorkflowStepSpec{{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Path: "/handle"}}}}}
		raw, _ := json.Marshal(def)
		draft = state.MutateForTest(t, author, app.ID, draft.Name, "save", draft.Version, raw, false)
		draft = state.MutateForTest(t, author, app.ID, draft.Name, "publish", draft.Version, nil, false)
		index, count, parent := 0, 1, "batch"
		suite.Cases[0].Simulation.Input = json.RawMessage(`{"items":[7]}`)
		suite.Cases[0].Live = &api.StoredWorkflowLiveTest{Expectations: []api.StoredWorkflowLiveExpectation{{Step: "batch/action", Loop: parent, ItemIndex: &index, State: "succeeded", Output: json.RawMessage(`null`), AttemptCount: &count}}}
		draft = saveSuiteForTest(t, author, app.ID, draft, &suite)
		live := store.(state.StoredWorkflowLiveStore)
		view, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), liveStartRequest(draft, dep), true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(t.Context(), view.RunID, []*state.WorkflowStep{{StepName: "batch"}, {StepName: api.WorkflowForEachItemName(parent, index), ForEachParent: &parent, ForEachIndex: &index}}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepStatus(t.Context(), view.RunID, "batch", state.WorkflowStepStatusSucceeded, 0, json.RawMessage(`"PRIVATE-AGGREGATE"`), nil); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepStatus(t.Context(), view.RunID, api.WorkflowForEachItemName(parent, index), state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`null`), nil); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(t.Context(), view.RunID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		checked, err := live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, view.RunID)
		if err != nil || checked.Report == nil || !checked.Report.Passed || checked.Report.Steps[0].Loop != "batch" {
			t.Fatal("loop item assertion used coordinator output", err)
		}
		zero := 0
		suite.Cases[0].Live = &api.StoredWorkflowLiveTest{ExpectedRunStatus: "failed", Expectations: []api.StoredWorkflowLiveExpectation{{Step: "batch", State: "skipped", AttemptCount: &zero}}}
		draft = saveSuiteForTest(t, author, app.ID, draft, &suite)
		cancelled, _, err := live.StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), liveStartRequest(draft, dep), true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(t.Context(), cancelled.RunID, []*state.WorkflowStep{{StepName: "batch"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CancelWorkflowRun(t.Context(), cancelled.RunID, "PRIVATE-CANCEL-REASON"); err != nil {
			t.Fatal(err)
		}
		checked, err = live.CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, cancelled.RunID)
		if err != nil || checked.Report == nil || checked.Report.Passed || checked.Report.Issue != "run_cancelled" || !checked.Report.Steps[0].Passed {
			t.Fatal("cancelled run passed", err)
		}
	})
}
