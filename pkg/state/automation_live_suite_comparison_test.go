// adr: 1298
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationLiveSuiteComparisonAcrossDeployments(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, request := seedLiveSuite(t, store)
		baseline := saveComparisonRuns(t, store, app, dep, draft, request, map[string]string{"real main": "succeeded", "second": "failed"})
		next, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:comparison-next", Status: state.DeployPending, Workflows: json.RawMessage(`[]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), next.ID); err != nil {
			t.Fatal(err)
		}
		request.ExpectedDeploymentID = next.ID
		candidate := saveComparisonRuns(t, store, app, next, draft, request, map[string]string{"real main": "failed", "second": "succeeded"})
		compare := reopen().(state.AutomationLiveSuiteComparisonStore)
		view, err := compare.CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, candidate.ID, baseline.ID)
		if err != nil || view.Summary != (api.AutomationLiveSuiteComparisonCounts{Total: 2, Regressions: 1, Recoveries: 1}) || !view.BaselineOutdated || view.CandidateOutdated || view.Cases[0].Change != "regression" || view.Cases[1].Change != "recovery" {
			t.Fatalf("historical comparison: %+v %v", view, err)
		}
		if view.Cases[0].Baseline.Result != "passed" || view.Cases[0].Candidate.Result != "failed" || view.Baseline.ID != baseline.ID || uuid.MustParse(view.Candidate.DeploymentID) != uuid.MustParse(next.ID) {
			t.Fatalf("comparison lost original identities/results: %+v", view)
		}
		for i, entry := range view.Cases {
			if entry.Candidate.CreatedAt == nil || !entry.Candidate.CreatedAt.Equal(candidate.Cases[i].Verification.CreatedAt) || entry.Baseline.CreatedAt == nil || !entry.Baseline.CreatedAt.Equal(baseline.Cases[i].Verification.CreatedAt) {
				t.Fatal("comparison lost original verification creation time", entry)
			}
		}
		reverse, err := compare.CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, baseline.ID, candidate.ID)
		if err != nil || reverse.Cases[0].Change != "recovery" || reverse.Cases[1].Change != "regression" {
			t.Fatal("comparison direction was ignored", err)
		}
		self, err := compare.CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, baseline.ID, baseline.ID)
		if err != nil || self.Summary.Unchanged != 2 || self.Cases[1].Candidate.Result != "failed" {
			t.Fatal("unchanged failure was hidden", err)
		}
		for _, scope := range [][4]string{{uuid.NewString(), draft.Name, candidate.ID, baseline.ID}, {app.ID, "foreign", candidate.ID, baseline.ID}, {app.ID, draft.Name, candidate.ID, uuid.NewString()}, {app.ID, draft.Name, uuid.NewString(), baseline.ID}, {app.ID, draft.Name, candidate.ID, "invalid"}, {app.ID, draft.Name, uuid.Nil.String(), baseline.ID}} {
			if _, err := compare.CompareAutomationLiveSuites(t.Context(), scope[0], scope[1], scope[2], scope[3]); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("unavailable/cross-scope comparison succeeded", err)
			}
		}
		foreign, _, foreignDraft, foreignRequest := seedLiveSuite(t, store)
		foreignSuite, err := store.(state.AutomationLiveSuiteStore).SaveAutomationLiveSuite(t.Context(), foreign.ID, foreignDraft.Name, uuid.NewString(), foreignRequest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compare.CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, candidate.ID, foreignSuite.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign baseline entered comparison", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := compare.CompareAutomationLiveSuites(cancelled, app.ID, draft.Name, candidate.ID, baseline.ID); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled comparison succeeded", err)
		}
		fresh, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, baseline.ID)
		if err != nil || !fresh.UpdatedAt.Equal(baseline.UpdatedAt) || !fresh.Cases[0].Verification.Report.CheckedAt.Equal(baseline.Cases[0].Verification.Report.CheckedAt) {
			t.Fatal("comparison changed suite metadata or assertion evidence", err)
		}
		runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 4 {
			t.Fatal("read admitted execution", err)
		}
		raw, _ := json.Marshal(view)
		for _, private := range []string{"PRIVATE", `"input":`, `"output":`, `"expectations":`, `"idempotency_key":`, `"steps":`} {
			if strings.Contains(string(raw), private) {
				t.Fatal("comparison leaked private evidence", private)
			}
		}
	})
}

func TestAutomationLiveSuiteComparisonIncompleteAndIncompatibleEvidence(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, request := seedLiveSuite(t, store)
		var saved api.AutomationTestSuite
		if err := json.Unmarshal(draft.TestSuite, &saved); err != nil {
			t.Fatal(err)
		}
		prototype := saved.Cases[0]
		saved.Cases = nil
		request.Cases = nil
		for _, name := range []string{"input", "assertion", "mock", "waiting", "unchecked", "stale", "unstarted", "removed"} {
			c := prototype
			c.Name = name
			saved.Cases = append(saved.Cases, c)
			request.Cases = append(request.Cases, api.AutomationLiveSuiteMember{Name: name})
		}
		draft = saveSharedSuiteCases(t, store.(state.AutomationStore), app.ID, draft, &saved)
		request.ExpectedVersion = draft.Version
		baseline := saveComparisonRuns(t, store, app, dep, draft, request, map[string]string{"input": "succeeded", "assertion": "succeeded", "mock": "succeeded", "waiting": "succeeded", "unchecked": "succeeded", "stale": "failed", "removed": "succeeded"})
		// Decode again to give each live-assertion slice independent ownership.
		saved = api.AutomationTestSuite{}
		if err := json.Unmarshal(draft.TestSuite, &saved); err != nil {
			t.Fatal(err)
		}
		saved.Cases[0].Simulation.Input = json.RawMessage(`{"changed":"PRIVATE-CHANGED"}`)
		saved.Cases[1].Live.Expectations[0].Output = json.RawMessage(`"PRIVATE-ASSERTION"`)
		saved.Cases[2].Simulation.MockOutputs = map[string]json.RawMessage{"main": json.RawMessage(`"PRIVATE-MOCK"`)}
		saved.Cases[len(saved.Cases)-1].Name = "added"
		draft = saveSharedSuiteCases(t, store.(state.AutomationStore), app.ID, draft, &saved)
		request.ExpectedVersion = draft.Version
		request.Cases[len(request.Cases)-1].Name = "added"
		candidate := saveComparisonRuns(t, store, app, dep, draft, request, map[string]string{"input": "failed", "assertion": "succeeded", "mock": "pending", "waiting": "pending", "unchecked": "unchecked", "stale": "failed", "added": "succeeded"})
		staleID := candidate.Cases[5].RunID
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(t.Context(), state.WorkflowResumeOptions{RunID: staleID, AppID: app.ID, AccountID: app.AccountID, ExpectedResumeCount: 0}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(t.Context(), staleID, "failed", nil, nil); err != nil {
			t.Fatal(err)
		}
		compare := reopen().(state.AutomationLiveSuiteComparisonStore)
		view, err := compare.CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, candidate.ID, baseline.ID)
		want := api.AutomationLiveSuiteComparisonCounts{Total: 9, Incompatible: 3, Unknown: 4, Added: 1, Removed: 1}
		if err != nil || view.Summary != want {
			t.Fatalf("incomplete evidence: %+v %v", view, err)
		}
		for i, result := range []string{"failed", "failed", "pending", "pending", "unavailable", "unavailable", "not_started", "passed"} {
			if view.Cases[i].Candidate.Result != result {
				t.Fatalf("case %d result: %+v", i, view.Cases[i])
			}
		}
		if view.Cases[7].Baseline != nil || view.Cases[8].Name != "removed" || view.Cases[8].Candidate != nil || view.Cases[6].Candidate.RunID != "" {
			t.Fatal("selection changes confused with unstarted cases")
		}
		fresh, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, candidate.ID)
		if err != nil || fresh.Cases[4].Verification.Report != nil || fresh.Cases[5].Verification.ReportCurrent {
			t.Fatal("comparison minted/revalidated reports", err)
		}
		if _, err := store.SweepExpiredWorkflowRuns(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		missing, err := compare.CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, candidate.ID, baseline.ID)
		if err != nil || missing.Summary.Unknown != 7 || missing.Summary.Incompatible != 0 || missing.Cases[0].Candidate.RunID != candidate.Cases[0].RunID || missing.Cases[0].Candidate.Result != "unavailable" {
			t.Fatal("expired evidence certified a result or lost identity", err, missing.Summary)
		}
	})
}

func TestAutomationLiveSuiteComparisonChangedDefinition(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, request := seedLiveSuite(t, store)
		baseline := saveComparisonRuns(t, store, app, dep, draft, request, map[string]string{"real main": "succeeded", "second": "succeeded"})
		draft = mutateSuiteForTest(t, store.(state.AutomationStore), app.ID, draft.Name, "save", draft.Version, suiteDraft(draft.Name, "/changed-definition", ""), false)
		draft = mutateSuiteForTest(t, store.(state.AutomationStore), app.ID, draft.Name, "publish", draft.Version, nil, false)
		request.ExpectedVersion, request.ExpectedPublishedVersion = draft.Version, draft.PublishedVersion
		candidate := saveComparisonRuns(t, store, app, dep, draft, request, map[string]string{"real main": "failed", "second": "succeeded"})
		view, err := reopen().(state.AutomationLiveSuiteComparisonStore).CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, candidate.ID, baseline.ID)
		if err != nil || view.Summary.Incompatible != 2 || view.Summary.Regressions != 0 {
			t.Fatal("different definitions compared as equal checks", err, view.Summary)
		}
	})
}

func TestAutomationLiveSuiteComparisonBoundedSelectionUnion(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, _, draft, request := seedLiveSuite(t, store)
		var prototype api.AutomationTestSuite
		if err := json.Unmarshal(draft.TestSuite, &prototype); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, prefix := range []string{"before ", "after "} {
			cases := api.AutomationTestSuite{}
			request.Cases = nil
			for i := range api.AutomationLiveSuiteMaxCases {
				c := prototype.Cases[0]
				c.Name = prefix + strings.Repeat("x", i+1)
				cases.Cases = append(cases.Cases, c)
				request.Cases = append(request.Cases, api.AutomationLiveSuiteMember{Name: c.Name})
			}
			draft = saveSharedSuiteCases(t, store.(state.AutomationStore), app.ID, draft, &cases)
			request.ExpectedVersion = draft.Version
			view, err := store.(state.AutomationLiveSuiteStore).SaveAutomationLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), request)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, view.ID)
		}
		view, err := reopen().(state.AutomationLiveSuiteComparisonStore).CompareAutomationLiveSuites(t.Context(), app.ID, draft.Name, ids[1], ids[0])
		if err != nil || len(view.Cases) != api.AutomationLiveSuiteComparisonMaxCases || view.Summary != (api.AutomationLiveSuiteComparisonCounts{Total: api.AutomationLiveSuiteComparisonMaxCases, Added: api.AutomationLiveSuiteMaxCases, Removed: api.AutomationLiveSuiteMaxCases}) {
			t.Fatal("comparison union was truncated or misclassified", err, view.Summary)
		}
	})
}

func saveComparisonRuns(t *testing.T, store state.Store, app state.App, dep state.Deployment, draft state.Automation, request api.SaveAutomationLiveSuiteRequest, statuses map[string]string) api.AutomationLiveSuite {
	t.Helper()
	request = cloneSharedSuiteRequest(request)
	for i, member := range request.Cases {
		status := statuses[member.Name]
		if status == "" {
			continue
		}
		live := startSuiteMember(t, store, app, dep, draft, member.Name)
		request.Cases[i].RunID = live.RunID
		if status == "pending" {
			continue
		}
		terminal := status
		if status == "unchecked" {
			terminal = "succeeded"
		}
		finishSharedLiveRun(t, store, live.RunID, json.RawMessage(`null`), 2, terminal)
		if status != "unchecked" {
			if _, err := store.(state.AutomationLiveStore).CheckAutomationLiveVerification(t.Context(), app.ID, draft.Name, live.RunID); err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err := store.(state.AutomationLiveSuiteStore).SaveAutomationLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), request)
	if err != nil {
		t.Fatal(err)
	}
	return view
}
