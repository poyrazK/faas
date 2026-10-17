// adr: 1304
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationLiveBaselineListingReadOnlyAndScoped(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		listing := reopen().(state.AutomationLiveBaselineListingStore)
		empty, err := listing.ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || empty.Baselines == nil || len(empty.Baselines) != 0 || empty.ObservedAt.IsZero() {
			t.Fatal("empty listing", err)
		}
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		request := baselinePromotionRequest(suite)
		promote := reopen().(state.AutomationLiveBaselineStore)
		for _, name := range []string{"staging", "production"} {
			if _, err := promote.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, name, uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
		}
		request.ExpectedRevision = 1
		if _, err := promote.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", request); err != nil {
			t.Fatal(err)
		}
		view, err := listing.ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || len(view.Baselines) != 2 || view.Baselines[0].Baseline.BaselineName != "production" || view.Baselines[0].Baseline.Revision != 2 || view.Baselines[1].Baseline.BaselineName != "staging" {
			t.Fatal("wrong heads/order", err)
		}
		for _, entry := range view.Baselines {
			if entry.EvidenceStatus != "usable" || entry.RenewalStatus != "passed" || len(entry.Cases) != 2 || entry.AgeSeconds == nil {
				t.Fatal("unhealthy valid evidence", entry)
			}
		}
		view.Baselines[0].Baseline.Policy[0] = '!'
		again, err := reopen().(state.AutomationLiveBaselineListingStore).ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || again.Baselines[0].Baseline.Policy[0] != '{' {
			t.Fatal("listing mutated stored receipt", err)
		}
		// Historical frozen reports remain usable even after a draft edit.
		updated, err := store.(state.AutomationStore).MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "save", ExpectedVersion: draft.Version, Draft: suiteDraft(draft.Name, "/changed", "")})
		if err != nil {
			t.Fatal(err)
		}
		historical, err := listing.ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || !historical.Baselines[0].Outdated || historical.Baselines[0].EvidenceStatus != "usable" || historical.Baselines[0].RenewalStatus != "inconclusive" {
			t.Fatal("historical target invalidated baseline", err)
		}
		// A run state change invalidates its old assertion report. Reading must not check it.
		if err := store.MarkWorkflowRunStatus(t.Context(), suite.Cases[0].RunID, "succeeded", nil, nil); err != nil {
			t.Fatal(err)
		}
		invalidated, err := listing.ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || invalidated.Baselines[0].EvidenceStatus != "unavailable" || invalidated.Baselines[0].Cases[0].Status != "unavailable" {
			t.Fatal("old report trusted", err)
		}
		retained, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, suite.ID)
		if err != nil || !retained.UpdatedAt.Equal(suite.UpdatedAt) || !retained.Cases[1].Verification.Report.CheckedAt.Equal(suite.Cases[1].Verification.Report.CheckedAt) {
			t.Fatal("health read changed evidence", err)
		}
		for _, scope := range [][2]string{{uuid.NewString(), draft.Name}, {app.ID, "foreign"}} {
			if _, err := listing.ListAutomationLiveBaselines(t.Context(), scope[0], scope[1]); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-scope discovery", err)
			}
		}
		raw, _ := json.Marshal(invalidated)
		for _, private := range []string{"PRIVATE", `"input":`, `"output":`, `"expectations":`, `"steps":`} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private payload in health", private)
			}
		}
		runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(runs) != 2 {
			t.Fatal("listing admitted execution", err)
		}
		if _, err := store.(state.AutomationStore).MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: updated.Version}); err != nil {
			t.Fatal(err)
		}
		missing, err := listing.ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || len(missing.Baselines) != 2 || missing.Baselines[0].EvidenceStatus != "unavailable" || len(missing.Baselines[0].Reasons) != 1 || missing.Baselines[0].Reasons[0] != "suite_unavailable" {
			t.Fatal("missing evidence hid audit heads", err)
		}

	})
}
