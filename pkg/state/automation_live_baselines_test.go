// adr: 1302
package state_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func baselinePromotionRequest(view api.AutomationLiveSuite) api.PromoteAutomationLiveBaselineRequest {
	return api.PromoteAutomationLiveBaselineRequest{SuiteID: view.ID, ExpectedVersion: view.Version, ExpectedPublishedVersion: view.PublishedVersion, ExpectedDeploymentID: view.DeploymentID, Policy: json.RawMessage(`{"version":2,"required_cases":["real main","second"],"max_candidate_age_seconds":3600}`)}
}
func TestAutomationLiveBaselinePromotionAndReplay(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.AutomationLiveBaselineStore)
		request := baselinePromotionRequest(suite)
		id := uuid.NewString()
		key := uuid.NewString()
		receipt, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, request)
		if err != nil || receipt.Revision != 1 || receipt.ActorAccountID != app.AccountID || receipt.ActorAPIKeyID != key || receipt.Suite.ID != suite.ID || receipt.CasesPassed != 2 || receipt.PolicyHash == "" || receipt.PromotedAt.IsZero() || receipt.OldestRunCreatedAt.IsZero() {
			t.Fatalf("receipt: %+v %v", receipt, err)
		}
		before, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil {
			t.Fatal(err)
		}
		read, err := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production")
		if err != nil || read.PromotionID != id {
			t.Fatal("head lost", err)
		}
		read.Policy[0] = '!'
		if again, err := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production"); err != nil || again.Policy[0] != '{' {
			t.Fatal("read mutated stored policy", err)
		}
		// A later promotion must not change the receipt returned by an old retry.
		next := request
		next.ExpectedRevision = 1
		second, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, key, next)
		if err != nil || second.Revision != 2 || second.PreviousPromotionID != id {
			t.Fatal("advance", err)
		}
		replay, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, request)
		if err != nil || replay.Revision != 1 || replay.PromotionID != id {
			t.Fatal("retry changed receipt", err)
		}
		head, _ := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production")
		if head.Revision != 2 {
			t.Fatal("old retry reset head")
		}
		for _, mode := range []string{"revision", "actor", "key", "policy", "alias"} {
			modified := request
			actor, actorKey, alias := app.AccountID, key, "production"
			switch mode {
			case "revision":
				modified.ExpectedRevision = 1
			case "actor":
				actor = uuid.NewString()
			case "key":
				actorKey = uuid.NewString()
			case "policy":
				modified.Policy = json.RawMessage(`{"version":2,"required_cases":["real main"],"max_candidate_age_seconds":3600}`)
			case "alias":
				alias = "staging"
			}
			if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, alias, id, actor, actorKey, modified); !errors.Is(err, state.ErrAutomationLiveBaselineConflict) {
				t.Fatalf("changed %s replay accepted: %v", mode, err)
			}
		}
		history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || len(history.Promotions) != 2 || history.Promotions[0].Revision != 2 {
			t.Fatal("history", err)
		}
		for _, scope := range [][3]string{{uuid.NewString(), draft.Name, "production"}, {app.ID, "foreign", "production"}, {app.ID, draft.Name, "foreign"}} {
			if _, err := baselines.GetAutomationLiveBaseline(t.Context(), scope[0], scope[1], scope[2]); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-scope read", err)
			}
		}
		after, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if err != nil || len(before) != len(after) {
			t.Fatal("baseline operation executed workflows", err)
		}
	})
}

func TestAutomationLiveBaselineRejectsUnsuitableEvidence(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		for _, mode := range []string{"failed", "pending", "unchecked", "unstarted", "coverage", "outdated", "wrong-target", "v1"} {
			t.Run(mode, func(t *testing.T) {
				app, dep, draft, plan := seedLiveSuite(t, store)
				states := map[string]string{"real main": "succeeded", "second": "succeeded"}
				switch mode {
				case "failed":
					states["second"] = "failed"
				case "pending":
					states["second"] = "pending"
				case "unchecked":
					states["second"] = "unchecked"
				case "unstarted":
					delete(states, "second")
				}
				suite := saveComparisonRuns(t, store, app, dep, draft, plan, states)
				request := baselinePromotionRequest(suite)
				if mode == "coverage" {
					request.Policy = json.RawMessage(`{"version":2,"required_cases":["never-selected"],"max_candidate_age_seconds":3600}`)
				}
				if mode == "v1" {
					request.Policy = json.RawMessage(`{"version":1,"required_cases":["real main"]}`)
				}
				if mode == "wrong-target" {
					request.ExpectedVersion++
				}
				if mode == "outdated" {
					_ = mutateSuiteForTest(t, store.(state.AutomationStore), app.ID, draft.Name, "save", draft.Version, suiteDraft(draft.Name, "/new", ""), false)
				}
				baselines := reopen().(state.AutomationLiveBaselineStore)
				if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", request); err == nil {
					t.Fatal("unsuitable evidence promoted")
				}
				if _, err := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production"); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("failed promotion left a head", err)
				}
			})
		}
	})
}

func TestAutomationLiveBaselineConcurrentPromotion(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		request := baselinePromotionRequest(suite)
		baselines := reopen().(state.AutomationLiveBaselineStore)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Go(func() {
				_, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", request)
				results <- err
			})
		}
		wg.Wait()
		close(results)
		successes, conflicts := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, state.ErrAutomationLiveBaselineConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes %d conflicts %d", successes, conflicts)
		}
		history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || len(history.Promotions) != 1 {
			t.Fatal("race left duplicate audit records", err)
		}
	})
}

func TestAutomationLiveBaselineBoundsAndPinning(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		request := baselinePromotionRequest(suite)
		baselines := reopen().(state.AutomationLiveBaselineStore)
		for revision := 0; revision < api.AutomationLiveBaselineHistoryLimit+1; revision++ {
			request.ExpectedRevision = int64(revision)
			if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
		}
		history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || len(history.Promotions) != api.AutomationLiveBaselineHistoryLimit || history.Promotions[len(history.Promotions)-1].Revision != 2 {
			t.Fatal("unbounded history", err)
		}
		// Create metadata-only suites; pruning cannot discard the current baseline.
		for range api.AutomationLiveSuiteRetainedLimit + 1 {
			if _, err := store.(state.AutomationLiveSuiteStore).SaveAutomationLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), plan); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); err != nil {
			t.Fatal("current baseline suite pruned", err)
		}
		request.ExpectedRevision = 0
		for i := 1; i < api.AutomationLiveBaselineLimit; i++ {
			if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, fmt.Sprintf("track-%d", i), uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "one-too-many", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("unbounded aliases")
		}
		raw, _ := json.Marshal(history)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("audit exposed workflow payloads")
		}
	})
}

func TestAutomationLiveBaselineAuditSurvivesEvidenceClear(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.AutomationLiveBaselineStore)
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		cleared, err := store.(state.AutomationStore).MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: draft.Version})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production"); err != nil {
			t.Fatal("clearing evidence erased audit", err)
		}
		if _, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("explicit evidence removal ignored", err)
		}
		if _, err := store.(state.AutomationStore).MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "delete", ExpectedVersion: cleared.Version}); err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production"); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("automation deletion retained scoped audit", err)
		}
	})
}

func TestAutomationLiveBaselineCanonicalPolicyBound(t *testing.T) {
	names := []string{}
	for i := 0; i < api.AutomationLiveSuiteMaxCases; i++ {
		names = append(names, strings.Repeat("&", 120)+fmt.Sprintf("%02d", i))
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"version": 2, "required_cases": names, "max_candidate_age_seconds": 3600}); err != nil {
		t.Fatal(err)
	}
	request := api.PromoteAutomationLiveBaselineRequest{SuiteID: uuid.NewString(), ExpectedVersion: 1, ExpectedPublishedVersion: 1, ExpectedDeploymentID: uuid.NewString(), Policy: buffer.Bytes()}
	_, err := state.NewMemStore().PromoteAutomationLiveBaseline(t.Context(), uuid.NewString(), "report", "production", uuid.NewString(), uuid.NewString(), "", request)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Status != 413 || problem.Limit == nil || problem.Observed == nil || problem.DocsURL == "" {
		t.Fatal("oversized canonical policy escaped pre-write validation", err)
	}
}

func TestAutomationLiveBaselineConcurrentIdenticalRetry(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		request := baselinePromotionRequest(suite)
		baselines := reopen().(state.AutomationLiveBaselineStore)
		id := uuid.NewString()
		var wg sync.WaitGroup
		type result struct {
			receipt api.AutomationLiveBaseline
			err     error
		}
		results := make(chan result, 2)
		for range 2 {
			wg.Go(func() {
				receipt, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, "", request)
				results <- result{receipt, err}
			})
		}
		wg.Wait()
		close(results)
		for got := range results {
			if got.err != nil || got.receipt.PromotionID != id || got.receipt.Revision != 1 {
				t.Fatalf("identical retry failed: %+v %v", got.receipt, got.err)
			}
		}
		history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || len(history.Promotions) != 1 {
			t.Fatal("identical retry duplicated audit", err)
		}
	})
}
