// adr: 1305
package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationLiveBaselineRetirementLifecycle(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.AutomationLiveBaselineStore)
		retire := reopen().(state.AutomationLiveBaselineRetirementStore)
		request := baselinePromotionRequest(suite)
		promotionID := uuid.NewString()
		_, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", promotionID, app.AccountID, "", request)
		if err != nil {
			t.Fatal(err)
		}
		// Compare the retained receipt: PostgreSQL JSONB normalizes policy
		// formatting without changing the policy or its canonical hash.
		original, err := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production")
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < api.AutomationLiveBaselineLimit; i++ {
			if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, fmt.Sprintf("track-%d", i), uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "overflow", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("active quota not enforced")
		}
		id := uuid.NewString()
		key := uuid.NewString()
		if _, err := retire.RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 2}); !errors.Is(err, state.ErrAutomationLiveBaselineConflict) {
			t.Fatal("stale retirement", err)
		}
		receipt, err := retire.RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1})
		if err != nil || receipt.Revision != 2 || receipt.PreviousPromotionID != promotionID || receipt.ActorAPIKeyID != key || receipt.RetiredAt.IsZero() {
			t.Fatal("retirement", receipt, err)
		}
		retry, err := reopen().(state.AutomationLiveBaselineRetirementStore).RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1})
		if err != nil || !reflect.DeepEqual(receipt, retry) {
			t.Fatal("retirement replay", err)
		}
		if _, err := retire.RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1}); !errors.Is(err, state.ErrAutomationLiveBaselineConflict) {
			t.Fatal("changed actor accepted", err)
		}
		if _, err := retire.RetireAutomationLiveBaseline(t.Context(), uuid.NewString(), draft.Name, "production", id, app.AccountID, key, api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1}); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross app receipt", err)
		}
		if _, err := baselines.GetAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production"); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatal("retired head usable", err)
		}
		replay, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", promotionID, app.AccountID, "", request)
		if err != nil || !reflect.DeepEqual(original, replay) {
			t.Fatal("historical promotion replay", err)
		}
		request.ExpectedRevision = 1
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("reactivated retired name")
		}
		request.ExpectedRevision = 0
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "replacement", uuid.NewString(), app.AccountID, "", request); err != nil {
			t.Fatal("active slot not released", err)
		}
		list, err := reopen().(state.AutomationLiveBaselineListingStore).ListAutomationLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || len(list.Baselines) != api.AutomationLiveBaselineLimit {
			t.Fatal("active list", err)
		}
		for _, entry := range list.Baselines {
			if entry.Baseline.BaselineName == "production" {
				t.Fatal("retired name still active")
			}
		}
		if _, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); err != nil {
			t.Fatal("retirement immediately deleted evidence", err)
		}
		history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || history.Retirement == nil || !reflect.DeepEqual(receipt, *history.Retirement) || !reflect.DeepEqual(history.Promotions[0], original) {
			t.Fatal("audit changed", err)
		}
		raw, _ := json.Marshal(history)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private audit payload")
		}
		// Explicit evidence clearing preserves retirement; automation deletion removes it.
		cleared, err := store.(state.AutomationStore).MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: draft.Version})
		if err != nil {
			t.Fatal(err)
		}
		history, err = baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || history.Retirement == nil {
			t.Fatal("evidence clear erased retirement", err)
		}
		if _, err := store.(state.AutomationStore).MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "delete", ExpectedVersion: cleared.Version}); err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production"); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("deleted automation retains audit", err)
		}
	})
}

func TestAutomationLiveBaselineRetirementUnpins(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.AutomationLiveBaselineStore)
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "old", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "shared", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().(state.AutomationLiveBaselineRetirementStore).RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "old", uuid.NewString(), app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
		for range api.AutomationLiveSuiteRetainedLimit + 1 {
			if _, err := reopen().SaveAutomationLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), plan); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); err != nil {
			t.Fatal("another active name lost its shared pin", err)
		}
		if _, err := reopen().(state.AutomationLiveBaselineRetirementStore).RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "shared", uuid.NewString(), app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().SaveAutomationLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), plan); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().GetAutomationLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("retired evidence still pinned", err)
		}
		if history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, "old"); err != nil || history.Retirement == nil {
			t.Fatal("normal pruning erased audit", err)
		}
	})
}

func TestAutomationLiveBaselineRetirementPromotionRace(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.AutomationLiveBaselineStore)
		request := baselinePromotionRequest(suite)
		for i := range 8 {
			alias := fmt.Sprintf("race-%d", i)
			if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make(chan error, 2)
			wg.Go(func() {
				<-start
				r := request
				r.ExpectedRevision = 1
				_, err := reopen().(state.AutomationLiveBaselineStore).PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", r)
				errs <- err
			})
			wg.Go(func() {
				<-start
				_, err := reopen().(state.AutomationLiveBaselineRetirementStore).RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1})
				errs <- err
			})
			close(start)
			wg.Wait()
			close(errs)
			wins := 0
			for err := range errs {
				if err == nil {
					wins++
				} else if !errors.Is(err, state.ErrAutomationLiveBaselineConflict) && !strings.Contains(err.Error(), "retired") {
					t.Fatal(err)
				}
			}
			if wins != 1 {
				t.Fatal("stale lifecycle decisions committed", wins)
			}
			history, err := baselines.GetAutomationLiveBaselineHistory(t.Context(), app.ID, draft.Name, alias)
			if err != nil {
				t.Fatal(err)
			}
			if history.Retirement != nil && history.Promotions[0].Revision != 1 {
				t.Fatal("promotion crossed retirement")
			}
		}
	})
}

func TestAutomationLiveBaselineRetirementLifetimeBound(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.AutomationLiveBaselineStore)
		retire := reopen().(state.AutomationLiveBaselineRetirementStore)
		request := baselinePromotionRequest(suite)
		for i := range api.AutomationLiveBaselineLifetimeNameLimit {
			alias := fmt.Sprintf("archived-%d", i)
			if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(i, err)
			}
			if _, err := retire.RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
				t.Fatal(i, err)
			}
		}
		if _, err := baselines.PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "overflow", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("unbounded retired-name audit")
		}
	})
}

func TestAutomationLiveBaselineRetirementConcurrentReplay(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		if _, err := reopen().(state.AutomationLiveBaselineStore).PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		start := make(chan struct{})
		var wg sync.WaitGroup
		receipts := make(chan api.AutomationLiveBaselineRetirement, 6)
		for range 6 {
			wg.Go(func() {
				<-start
				receipt, err := reopen().(state.AutomationLiveBaselineRetirementStore).RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1})
				if err != nil {
					t.Error(err)
				}
				receipts <- receipt
			})
		}
		close(start)
		wg.Wait()
		close(receipts)
		var first *api.AutomationLiveBaselineRetirement
		for receipt := range receipts {
			if first == nil {
				first = &receipt
			} else if !reflect.DeepEqual(*first, receipt) {
				t.Fatal("concurrent retries did not converge")
			}
		}
	})
}

func TestAutomationLiveBaselineRetirementWithoutLiveDeployment(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.AutomationLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		if _, err := reopen().(state.AutomationLiveBaselineStore).PromoteAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().(state.AutomationLiveBaselineRetirementStore).RetireAutomationLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
			t.Fatal("retirement requires execution target", err)
		}
	})
}
