// adr: 1296
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedLiveSuite(t *testing.T, store state.Store) (state.App, state.Deployment, state.StoredWorkflow, api.SaveStoredWorkflowLiveSuiteRequest) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "suite-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:suite", Status: state.DeployPending, Workflows: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	author := store.(state.StoredWorkflowStore)
	draft := mutateSuiteForTest(t, author, app.ID, "live-cases", "save", 0, suiteDraft("live-cases", "/handler", ""), false)
	draft = mutateSuiteForTest(t, author, app.ID, draft.Name, "publish", draft.Version, nil, false)
	count := 2
	cases := api.StoredWorkflowTestSuite{}
	for _, name := range []string{"real main", "second"} {
		cases.Cases = append(cases.Cases, api.StoredWorkflowTestCase{Name: name, Simulation: api.StoredWorkflowTestInputs{Input: json.RawMessage(`{"private":"PRIVATE-INPUT"}`)}, Expectations: []api.StoredWorkflowCheckExpectation{{Step: "main", State: "mocked"}}, Live: &api.StoredWorkflowLiveTest{Expectations: []api.StoredWorkflowLiveExpectation{{Step: "main", State: "succeeded", Output: json.RawMessage(`null`), AttemptCount: &count}}}})
	}
	draft = saveSharedSuiteCases(t, author, app.ID, draft, &cases)
	request := api.SaveStoredWorkflowLiveSuiteRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: dep.ID, Cases: []api.StoredWorkflowLiveSuiteMember{{Name: "real main"}, {Name: "second"}}}
	return app, dep, draft, request
}

func startSuiteMember(t *testing.T, store state.Store, app state.App, dep state.Deployment, draft state.StoredWorkflow, name string) api.StoredWorkflowLiveVerification {
	t.Helper()
	req := api.StartStoredWorkflowLiveVerificationRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: dep.ID, ConfirmRealActions: true}
	req.CaseName = name
	view, _, err := store.(state.StoredWorkflowLiveStore).StartStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, uuid.NewString(), req, true)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestStoredWorkflowLiveSuitePersistentResultsAndFreshness(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, request := seedLiveSuite(t, store)
		var savedCases api.StoredWorkflowTestSuite
		if err := json.Unmarshal(draft.TestSuite, &savedCases); err != nil {
			t.Fatal(err)
		}
		savedCases.Cases[0].Live.ExpectedRunStatus = "failed"
		savedCases.Cases[0].Live.Expectations[0].State = "failed"
		draft = saveSharedSuiteCases(t, store.(state.StoredWorkflowStore), app.ID, draft, &savedCases)
		request.ExpectedVersion = draft.Version
		suites := store.(state.StoredWorkflowLiveSuiteStore)
		id := uuid.NewString()
		initial, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request)
		if err != nil || initial.Outcome != "incomplete" || initial.Summary.NotStarted != 2 {
			t.Fatalf("new suite: %+v %v", initial, err)
		}
		runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 0 {
			t.Fatal("saving metadata admitted real actions", err)
		}
		suites = reopen()
		first := startSuiteMember(t, store, app, dep, draft, request.Cases[0].Name)
		linked := cloneSharedSuiteRequest(request)
		linked.Cases[0].RunID = first.RunID
		pending, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, linked)
		if err != nil || pending.Summary.Pending != 1 || pending.Summary.NotStarted != 1 {
			t.Fatalf("link: %+v %v", pending, err)
		}
		staleWriter, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request)
		if err != nil || staleWriter.Cases[0].RunID != first.RunID || !staleWriter.UpdatedAt.Equal(pending.UpdatedAt) {
			t.Fatal("old writer erased progress or churned metadata", err)
		}
		finishSharedLiveRun(t, store, first.RunID, json.RawMessage(`null`), 2, "failed")
		unavailable, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || unavailable.Summary.Unavailable != 1 {
			t.Fatal("GET minted an assertion report", err)
		}
		if _, err := store.(state.StoredWorkflowLiveStore).CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, first.RunID); err != nil {
			t.Fatal(err)
		}
		second := startSuiteMember(t, store, app, dep, draft, "second")
		linked.Cases[1].RunID = second.RunID
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, linked); err != nil {
			t.Fatal(err)
		}
		finishSharedLiveRun(t, store, second.RunID, json.RawMessage(`null`), 2)
		if _, err := store.(state.StoredWorkflowLiveStore).CheckStoredWorkflowLiveVerification(t.Context(), app.ID, draft.Name, second.RunID); err != nil {
			t.Fatal(err)
		}
		passed, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || passed.Outcome != "passed" || passed.Summary.Passed != 2 || passed.Outdated {
			t.Fatalf("passing suite: %+v %v", passed, err)
		}
		raw, _ := json.Marshal(passed)
		for _, secret := range []string{"PRIVATE", `"input":`, `"output":`, `"expectations":`, `"idempotency_key":`} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("suite exposed payloads or private assertions", secret)
			}
		}
		passed.Cases[0].Verification.Report.Steps[0].Passed = false
		fresh, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || !fresh.Cases[0].Verification.Report.Steps[0].Passed {
			t.Fatal("suite response aliased retained records", err)
		}
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(t.Context(), state.WorkflowResumeOptions{RunID: first.RunID, AppID: app.ID, AccountID: app.AccountID, ExpectedResumeCount: 0}); err != nil {
			t.Fatal(err)
		}
		continued, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || continued.Outcome != "pending" || continued.Summary.Pending != 1 {
			t.Fatal("resumed run reused passing report", err)
		}
		current := mutateSuiteForTest(t, store.(state.StoredWorkflowStore), app.ID, draft.Name, "save", draft.Version, suiteDraft(draft.Name, "/edited", ""), false)
		outdated, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || outdated.Outcome != "outdated" || !outdated.Outdated {
			t.Fatal("changed stored workflow appeared current", err)
		}
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, linked); err != nil {
			t.Fatal("could not retain historical links after drift", err)
		}
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), request); !errors.Is(err, state.ErrStoredWorkflowVersionConflict) {
			t.Fatal("created a new suite against a stale target", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := suites.GetStoredWorkflowLiveSuite(cancelled, app.ID, draft.Name, id); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled suite read succeeded", err)
		}
		_ = saveSharedSuiteCases(t, store.(state.StoredWorkflowStore), app.ID, current, nil)
		if _, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("clearing tests retained shared live history", err)
		}
		if _, err := store.GetWorkflowRun(t.Context(), first.RunID); err != nil {
			t.Fatal("clearing history deleted execution", err)
		}
	})
}

func TestStoredWorkflowLiveSuiteIsolationAndMonotonicLinks(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, request := seedLiveSuite(t, store)
		suites := store.(state.StoredWorkflowLiveSuiteStore)
		id := uuid.NewString()
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request); err != nil {
			t.Fatal(err)
		}
		for _, scope := range [][2]string{{uuid.NewString(), draft.Name}, {app.ID, "other"}} {
			if _, err := suites.GetStoredWorkflowLiveSuite(t.Context(), scope[0], scope[1], id); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-scope suite read succeeded", err)
			}
		}
		for _, edit := range []func(*api.SaveStoredWorkflowLiveSuiteRequest){
			func(r *api.SaveStoredWorkflowLiveSuiteRequest) { r.ExpectedVersion++ },
			func(r *api.SaveStoredWorkflowLiveSuiteRequest) { r.Cases[0].Name = "changed" },
			func(r *api.SaveStoredWorkflowLiveSuiteRequest) { r.Cases[0], r.Cases[1] = r.Cases[1], r.Cases[0] },
			func(r *api.SaveStoredWorkflowLiveSuiteRequest) { r.Cases[0].RunID = uuid.NewString() },
		} {
			bad := cloneSharedSuiteRequest(request)
			edit(&bad)
			if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, bad); err == nil {
				t.Fatal("suite accepted retargeting or an unknown run")
			}
		}
		first := startSuiteMember(t, store, app, dep, draft, request.Cases[0].Name)
		second := startSuiteMember(t, store, app, dep, draft, "second")
		wrong := cloneSharedSuiteRequest(request)
		wrong.Cases[0].RunID = second.RunID
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, wrong); !errors.Is(err, state.ErrWorkflowRunIdempotencyConflict) {
			t.Fatal("attached another case's run", err)
		}
		var wg sync.WaitGroup
		for i, runID := range []string{first.RunID, second.RunID} {
			wg.Go(func() {
				update := cloneSharedSuiteRequest(request)
				update.Cases[i].RunID = runID
				if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, update); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		both, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || both.LinkedRuns != 2 || both.Cases[0].RunID != first.RunID || both.Cases[1].RunID != second.RunID {
			t.Fatal("concurrent sync lost run identities", err)
		}
		foreign, _, foreignDraft, foreignRequest := seedLiveSuite(t, store)
		foreignRequest.Cases[0].RunID = first.RunID
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), foreign.ID, foreignDraft.Name, uuid.NewString(), foreignRequest); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("attached a foreign app's run", err)
		}
	})
}

func TestStoredWorkflowLiveSuiteMissingRecordsAndRetention(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, request := seedLiveSuite(t, store)
		suites := store.(state.StoredWorkflowLiveSuiteStore)
		first := startSuiteMember(t, store, app, dep, draft, request.Cases[0].Name)
		request.Cases[0].RunID = first.RunID
		id := uuid.NewString()
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request); err != nil {
			t.Fatal(err)
		}
		finishSharedLiveRun(t, store, first.RunID, json.RawMessage(`null`), 2)
		if _, err := store.SweepExpiredWorkflowRuns(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		missing, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id)
		if err != nil || missing.Outcome != "unavailable" || missing.Cases[0].RunID != first.RunID || missing.Cases[0].Verification != nil {
			t.Fatal("expired run was hidden or counted as passing", err)
		}
		if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id, request); err != nil {
			t.Fatal("existing expired identity could not be retained", err)
		}
		request.Cases[0].RunID = ""
		var newest string
		for i := 0; i < api.StoredWorkflowLiveSuiteRetainedLimit; i++ {
			newest = uuid.NewString()
			if _, err := suites.SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, newest, request); err != nil {
				t.Fatal(err)
			}
		}
		list, err := suites.ListStoredWorkflowLiveSuites(t.Context(), app.ID, draft.Name)
		if err != nil || len(list.Suites) != api.StoredWorkflowLiveSuiteHistoryLimit || list.Suites[0].ID != newest {
			t.Fatal("suite listing was not bounded and ordered", err)
		}
		if _, err := suites.GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("history retention was unbounded", err)
		}
	})
}

func sharedSuiteStores(t *testing.T, run func(*testing.T, state.Store, func() state.StoredWorkflowLiveSuiteStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		store := state.NewMemStore()
		run(t, store, func() state.StoredWorkflowLiveSuiteStore { return store })
	})
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		run(t, state.NewPgStore(pool), func() state.StoredWorkflowLiveSuiteStore { return state.NewPgStore(pool) })
	})
}
func cloneSharedSuiteRequest(request api.SaveStoredWorkflowLiveSuiteRequest) api.SaveStoredWorkflowLiveSuiteRequest {
	request.Cases = append([]api.StoredWorkflowLiveSuiteMember(nil), request.Cases...)
	return request
}
func suiteDraft(name, path, _ string) json.RawMessage {
	raw, _ := json.Marshal(api.WorkflowSpec{Name: name, Trigger: &api.WorkflowTriggerSpec{Type: "manual"}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: path}}})
	return raw
}
func mutateSuiteForTest(t *testing.T, store state.StoredWorkflowStore, app, name, action string, version int64, raw json.RawMessage, takeover bool) state.StoredWorkflow {
	t.Helper()
	result, err := store.MutateStoredWorkflow(t.Context(), app, name, state.StoredWorkflowMutation{Action: action, ExpectedVersion: version, Draft: raw, TakeOverManifest: takeover})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func saveSharedSuiteCases(t *testing.T, store state.StoredWorkflowStore, app string, draft state.StoredWorkflow, cases *api.StoredWorkflowTestSuite) state.StoredWorkflow {
	t.Helper()
	var raw json.RawMessage
	if cases != nil {
		raw, _ = json.Marshal(cases)
	}
	result, err := store.MutateStoredWorkflow(t.Context(), app, draft.Name, state.StoredWorkflowMutation{Action: "tests", ExpectedVersion: draft.Version, TestSuite: raw})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func finishSharedLiveRun(t *testing.T, store state.Store, id string, output json.RawMessage, attempt int, terminalState ...string) {
	t.Helper()
	if err := store.CreateWorkflowSteps(t.Context(), id, []*state.WorkflowStep{{RunID: id, StepName: "main", Status: state.WorkflowStepStatusPending}}); err != nil {
		t.Fatal(err)
	}
	terminal := "succeeded"
	if len(terminalState) > 0 {
		terminal = terminalState[0]
	}
	if err := store.MarkWorkflowStepStatus(t.Context(), id, "main", terminal, attempt, output, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(t.Context(), id, terminal, json.RawMessage(`"PRIVATE-RUN-OUTPUT"`), nil); err != nil {
		t.Fatal(err)
	}
}
