// adr: 1297
package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func managedSuiteRequest(plan api.SaveStoredWorkflowLiveSuiteRequest) api.StartStoredWorkflowLiveSuiteRequest {
	request := api.StartStoredWorkflowLiveSuiteRequest{ExpectedVersion: plan.ExpectedVersion, ExpectedPublishedVersion: plan.ExpectedPublishedVersion, ExpectedDeploymentID: plan.ExpectedDeploymentID, ConfirmRealActions: true}
	for _, c := range plan.Cases {
		request.Cases = append(request.Cases, c.Name)
	}
	return request
}

func managedSuiteID(at time.Time) string {
	id := uuid.Must(uuid.NewV7())
	ms := at.UnixMilli()
	for i := 0; i < 6; i++ {
		id[5-i] = byte(ms >> (8 * i))
	}
	return id.String()
}

func TestStoredWorkflowLiveSuiteExecutionRecoversConcurrentProgress(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, _, draft, plan := seedLiveSuite(t, store)
		executor := store.(state.StoredWorkflowLiveSuiteExecutionStore)
		request, id := managedSuiteRequest(plan), managedSuiteID(time.Now())
		first, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true)
		if err != nil || !first.Managed || first.LinkedRuns != 1 || first.Cases[0].RunID == "" || first.Cases[1].RunID != "" {
			t.Fatalf("start: %+v %v", first, err)
		}
		replayed, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, false)
		if err != nil || replayed.Cases[0].RunID != first.Cases[0].RunID {
			t.Fatal("lost-response replay changed admission", err)
		}
		if _, err := store.(state.StoredWorkflowLiveSuiteStore).SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, plan); !errors.Is(err, state.ErrWorkflowRunIdempotencyConflict) {
			t.Fatal("metadata PUT took over execution", err)
		}
		if _, err := executor.AdvanceStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, api.AdvanceStoredWorkflowLiveSuiteRequest{}, true); err == nil {
			t.Fatal("continued without confirmation")
		}
		finishSharedLiveRun(t, store, first.Cases[0].RunID, json.RawMessage(`null`), 2)
		observed, err := reopen().GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || observed.LinkedRuns != 1 || observed.Cases[0].Outcome != "unavailable" {
			t.Fatal("browsing executed or checked a case", err)
		}
		executor = reopen().(state.StoredWorkflowLiveSuiteExecutionStore)
		var wg sync.WaitGroup
		views := make(chan api.StoredWorkflowLiveSuite, 8)
		for range 8 {
			wg.Go(func() {
				view, err := executor.AdvanceStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, api.AdvanceStoredWorkflowLiveSuiteRequest{ConfirmRealActions: true}, true)
				if err != nil {
					t.Error(err)
					return
				}
				views <- view
			})
		}
		wg.Wait()
		close(views)
		var second string
		for view := range views {
			if view.LinkedRuns != 2 || view.Cases[0].RunID != first.Cases[0].RunID || view.Cases[0].Outcome != "passed" {
				t.Fatalf("unsafe progress: %+v", view)
			}
			if second != "" && second != view.Cases[1].RunID {
				t.Fatal("concurrent tabs admitted replacements")
			}
			second = view.Cases[1].RunID
		}
		if second == "" {
			t.Fatal("no next case")
		}
		finishSharedLiveRun(t, store, second, json.RawMessage(`null`), 2)
		passed, err := executor.AdvanceStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, api.AdvanceStoredWorkflowLiveSuiteRequest{ConfirmRealActions: true}, true)
		if err != nil || passed.Outcome != "passed" || passed.Summary.Passed != 2 {
			t.Fatalf("final result: %+v %v", passed, err)
		}
		runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 2 {
			t.Fatal("suite admitted duplicate work", len(runs), err)
		}
	})
}

func TestStoredWorkflowLiveSuiteExecutionStopsAndValidates(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		for _, mode := range []string{"failed", "outdated", "disabled"} {
			t.Run(mode, func(t *testing.T) {
				app, _, draft, plan := seedLiveSuite(t, store)
				executor := store.(state.StoredWorkflowLiveSuiteExecutionStore)
				id, request := managedSuiteID(time.Now()), managedSuiteRequest(plan)
				first, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true)
				if err != nil {
					t.Fatal(err)
				}
				output := json.RawMessage(`null`)
				if mode == "failed" {
					output = json.RawMessage(`"wrong"`)
				}
				finishSharedLiveRun(t, store, first.Cases[0].RunID, output, 2)
				if mode == "outdated" {
					_ = mutateSuiteForTest(t, store.(state.StoredWorkflowStore), app.ID, draft.Name, "save", draft.Version, suiteDraft(draft.Name, "/edited", ""), false)
				}
				view, err := executor.AdvanceStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, api.AdvanceStoredWorkflowLiveSuiteRequest{ConfirmRealActions: true}, mode != "disabled")
				if mode != "disabled" && (err != nil || view.Outcome != mode || view.LinkedRuns != 1) {
					t.Fatalf("did not stop: %+v %v", view, err)
				}
				if mode == "disabled" && err == nil {
					t.Fatal("runtime gate did not block next admission")
				}
				runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
				if err != nil || len(runs) != 1 {
					t.Fatal("stop admitted later cases", len(runs), err)
				}
				changed := request
				changed.Cases = []string{request.Cases[1], request.Cases[0]}
				if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, changed, true); !errors.Is(err, state.ErrWorkflowRunIdempotencyConflict) {
					t.Fatal("retargeted start replay", err)
				}
			})
		}
		app, _, draft, plan := seedLiveSuite(t, store)
		executor := store.(state.StoredWorkflowLiveSuiteExecutionStore)
		request := managedSuiteRequest(plan)
		for _, id := range []string{uuid.NewString(), managedSuiteID(time.Now().Add(-2 * api.StoredWorkflowLiveSuiteStartWindow)), managedSuiteID(time.Now().Add(time.Hour))} {
			if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true); err == nil {
				t.Fatal("accepted invalid/expired creation ID")
			}
		}
		request.ConfirmRealActions = false
		if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, managedSuiteID(time.Now()), request, true); err == nil {
			t.Fatal("started without confirmation")
		}
		request.ConfirmRealActions = true
		request.Cases = []string{plan.Cases[0].Name, "missing"}
		if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, managedSuiteID(time.Now()), request, true); err == nil {
			t.Fatal("partly admitted invalid selection")
		}
		runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 0 {
			t.Fatal("invalid start performed work", len(runs), err)
		}
		if _, err := executor.AdvanceStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, managedSuiteID(time.Now()), api.AdvanceStoredWorkflowLiveSuiteRequest{ConfirmRealActions: true}, true); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("continuation recreated missing suite", err)
		}
	})
}

func TestStoredWorkflowLiveSuiteExecutionMissingRunsAndProtectedHistory(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, _, draft, plan := seedLiveSuite(t, store)
		executor := store.(state.StoredWorkflowLiveSuiteExecutionStore)
		id, request := managedSuiteID(time.Now()), managedSuiteRequest(plan)
		first, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true)
		if err != nil {
			t.Fatal(err)
		}
		finishSharedLiveRun(t, store, first.Cases[0].RunID, json.RawMessage(`null`), 2)
		if _, err := store.SweepExpiredWorkflowRuns(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		view, err := executor.AdvanceStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, api.AdvanceStoredWorkflowLiveSuiteRequest{ConfirmRealActions: true}, true)
		if err != nil || view.Outcome != "unavailable" || view.LinkedRuns != 1 || view.Cases[0].RunID != first.Cases[0].RunID {
			t.Fatalf("missing run replaced: %+v %v", view, err)
		}
		for range api.StoredWorkflowLiveSuiteRetainedLimit {
			if _, err := reopen().SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), plan); err != nil {
				t.Fatal(err)
			}
		}
		// A recent start receipt survives metadata churn until its UUIDv7 expires.
		replayed, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true)
		if err != nil || replayed.Cases[0].RunID != first.Cases[0].RunID || replayed.LinkedRuns != 1 {
			t.Fatal("history pruning lost execution recovery identity", err)
		}
		runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 0 {
			t.Fatal("missing run recovery admitted replacement work", len(runs), err)
		}
		foreign, _, otherDraft, otherPlan := seedLiveSuite(t, store)
		if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), foreign.ID, otherDraft.Name, id, managedSuiteRequest(otherPlan), true); err == nil {
			t.Fatal("foreign suite ID taken over")
		}
		runs, _, err = store.ListWorkflowRuns(t.Context(), foreign.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 0 {
			t.Fatal("conflicting identity did not roll back admission", len(runs), err)
		}
	})
}

func TestStoredWorkflowLiveSuiteAllowsBoundedFutureClockSkew(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, _ func() state.StoredWorkflowLiveSuiteStore) {
		app, _, draft, plan := seedLiveSuite(t, store)
		executor := store.(state.StoredWorkflowLiveSuiteExecutionStore)
		request := managedSuiteRequest(plan)
		id := managedSuiteID(time.Now().Add(5 * time.Second))
		if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true); err != nil {
			t.Fatalf("small future clock skew rejected: %v", err)
		}
		id = managedSuiteID(time.Now().Add(api.StoredWorkflowLiveSuiteFutureClockSkew + 30*time.Second))
		if _, err := executor.StartStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request, true); err == nil {
			t.Fatal("suite outside future clock skew was accepted")
		}
	})
}
