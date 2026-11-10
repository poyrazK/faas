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

func TestStoredWorkflowLiveBaselineRetirementLifecycle(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.StoredWorkflowLiveBaselineStore)
		retire := reopen().(state.StoredWorkflowLiveBaselineRetirementStore)
		request := baselinePromotionRequest(suite)
		promotionID := uuid.NewString()
		_, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", promotionID, app.AccountID, "", request)
		if err != nil {
			t.Fatal(err)
		}
		// Compare the retained receipt: PostgreSQL JSONB normalizes policy
		// formatting without changing the policy or its canonical hash.
		original, err := baselines.GetStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production")
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < api.StoredWorkflowLiveBaselineLimit; i++ {
			if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, fmt.Sprintf("track-%d", i), uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "overflow", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("active quota not enforced")
		}
		id := uuid.NewString()
		key := uuid.NewString()
		if _, err := retire.RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 2}); !errors.Is(err, state.ErrStoredWorkflowLiveBaselineConflict) {
			t.Fatal("stale retirement", err)
		}
		receipt, err := retire.RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1})
		if err != nil || receipt.Revision != 2 || receipt.PreviousPromotionID != promotionID || receipt.ActorAPIKeyID != key || receipt.RetiredAt.IsZero() {
			t.Fatal("retirement", receipt, err)
		}
		retry, err := reopen().(state.StoredWorkflowLiveBaselineRetirementStore).RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, key, api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1})
		if err != nil || !reflect.DeepEqual(receipt, retry) {
			t.Fatal("retirement replay", err)
		}
		if _, err := retire.RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1}); !errors.Is(err, state.ErrStoredWorkflowLiveBaselineConflict) {
			t.Fatal("changed actor accepted", err)
		}
		if _, err := retire.RetireStoredWorkflowLiveBaseline(t.Context(), uuid.NewString(), draft.Name, "production", id, app.AccountID, key, api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1}); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross app receipt", err)
		}
		if _, err := baselines.GetStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production"); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatal("retired head usable", err)
		}
		replay, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", promotionID, app.AccountID, "", request)
		if err != nil || !reflect.DeepEqual(original, replay) {
			t.Fatal("historical promotion replay", err)
		}
		request.ExpectedRevision = 1
		if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("reactivated retired name")
		}
		request.ExpectedRevision = 0
		if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "replacement", uuid.NewString(), app.AccountID, "", request); err != nil {
			t.Fatal("active slot not released", err)
		}
		list, err := reopen().(state.StoredWorkflowLiveBaselineListingStore).ListStoredWorkflowLiveBaselines(t.Context(), app.ID, draft.Name)
		if err != nil || len(list.Baselines) != api.StoredWorkflowLiveBaselineLimit {
			t.Fatal("active list", err)
		}
		for _, entry := range list.Baselines {
			if entry.Baseline.BaselineName == "production" {
				t.Fatal("retired name still active")
			}
		}
		if _, err := reopen().GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); err != nil {
			t.Fatal("retirement immediately deleted evidence", err)
		}
		history, err := baselines.GetStoredWorkflowLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || history.Retirement == nil || !reflect.DeepEqual(receipt, *history.Retirement) || !reflect.DeepEqual(history.Promotions[0], original) {
			t.Fatal("audit changed", err)
		}
		raw, _ := json.Marshal(history)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private audit payload")
		}
		// Explicit evidence clearing preserves retirement; automation deletion removes it.
		cleared, err := store.(state.StoredWorkflowStore).MutateStoredWorkflow(t.Context(), app.ID, draft.Name, state.StoredWorkflowMutation{Action: "tests", ExpectedVersion: draft.Version})
		if err != nil {
			t.Fatal(err)
		}
		history, err = baselines.GetStoredWorkflowLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production")
		if err != nil || history.Retirement == nil {
			t.Fatal("evidence clear erased retirement", err)
		}
		if _, err := store.(state.StoredWorkflowStore).MutateStoredWorkflow(t.Context(), app.ID, draft.Name, state.StoredWorkflowMutation{Action: "delete", ExpectedVersion: cleared.Version}); err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.GetStoredWorkflowLiveBaselineHistory(t.Context(), app.ID, draft.Name, "production"); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("deleted stored workflow retains audit", err)
		}
	})
}

func TestStoredWorkflowLiveBaselineRetirementUnpins(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.StoredWorkflowLiveBaselineStore)
		if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "old", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "shared", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().(state.StoredWorkflowLiveBaselineRetirementStore).RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "old", uuid.NewString(), app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
		for range api.StoredWorkflowLiveSuiteRetainedLimit + 1 {
			if _, err := reopen().SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), plan); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := reopen().GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); err != nil {
			t.Fatal("another active name lost its shared pin", err)
		}
		if _, err := reopen().(state.StoredWorkflowLiveBaselineRetirementStore).RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "shared", uuid.NewString(), app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().SaveStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, uuid.NewString(), plan); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().GetStoredWorkflowLiveSuite(t.Context(), app.ID, draft.Name, suite.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("retired evidence still pinned", err)
		}
		if history, err := baselines.GetStoredWorkflowLiveBaselineHistory(t.Context(), app.ID, draft.Name, "old"); err != nil || history.Retirement == nil {
			t.Fatal("normal pruning erased audit", err)
		}
	})
}

func TestStoredWorkflowLiveBaselineRetirementPromotionRace(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.StoredWorkflowLiveBaselineStore)
		request := baselinePromotionRequest(suite)
		for i := range 8 {
			alias := fmt.Sprintf("race-%d", i)
			if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make(chan error, 2)
			wg.Go(func() {
				<-start
				r := request
				r.ExpectedRevision = 1
				_, err := reopen().(state.StoredWorkflowLiveBaselineStore).PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", r)
				errs <- err
			})
			wg.Go(func() {
				<-start
				_, err := reopen().(state.StoredWorkflowLiveBaselineRetirementStore).RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1})
				errs <- err
			})
			close(start)
			wg.Wait()
			close(errs)
			wins := 0
			for err := range errs {
				if err == nil {
					wins++
				} else if !errors.Is(err, state.ErrStoredWorkflowLiveBaselineConflict) && !strings.Contains(err.Error(), "retired") {
					t.Fatal(err)
				}
			}
			if wins != 1 {
				t.Fatal("stale lifecycle decisions committed", wins)
			}
			history, err := baselines.GetStoredWorkflowLiveBaselineHistory(t.Context(), app.ID, draft.Name, alias)
			if err != nil {
				t.Fatal(err)
			}
			if history.Retirement != nil && history.Promotions[0].Revision != 1 {
				t.Fatal("promotion crossed retirement")
			}
		}
	})
}

func TestStoredWorkflowLiveBaselineRetirementLifetimeBound(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		baselines := reopen().(state.StoredWorkflowLiveBaselineStore)
		retire := reopen().(state.StoredWorkflowLiveBaselineRetirementStore)
		request := baselinePromotionRequest(suite)
		for i := range api.StoredWorkflowLiveBaselineLifetimeNameLimit {
			alias := fmt.Sprintf("archived-%d", i)
			if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", request); err != nil {
				t.Fatal(i, err)
			}
			if _, err := retire.RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, alias, uuid.NewString(), app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
				t.Fatal(i, err)
			}
		}
		if _, err := baselines.PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "overflow", uuid.NewString(), app.AccountID, "", request); err == nil {
			t.Fatal("unbounded retired-name audit")
		}
	})
}

func TestStoredWorkflowLiveBaselineRetirementConcurrentReplay(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		if _, err := reopen().(state.StoredWorkflowLiveBaselineStore).PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		start := make(chan struct{})
		var wg sync.WaitGroup
		receipts := make(chan api.StoredWorkflowLiveBaselineRetirement, 6)
		for range 6 {
			wg.Go(func() {
				<-start
				receipt, err := reopen().(state.StoredWorkflowLiveBaselineRetirementStore).RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", id, app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1})
				if err != nil {
					t.Error(err)
				}
				receipts <- receipt
			})
		}
		close(start)
		wg.Wait()
		close(receipts)
		var first *api.StoredWorkflowLiveBaselineRetirement
		for receipt := range receipts {
			if first == nil {
				first = &receipt
			} else if !reflect.DeepEqual(*first, receipt) {
				t.Fatal("concurrent retries did not converge")
			}
		}
	})
}

func TestStoredWorkflowLiveBaselineRetirementWithoutLiveDeployment(t *testing.T) {
	sharedSuiteStores(t, func(t *testing.T, store state.Store, reopen func() state.StoredWorkflowLiveSuiteStore) {
		app, dep, draft, plan := seedLiveSuite(t, store)
		suite := saveComparisonRuns(t, store, app, dep, draft, plan, map[string]string{"real main": "succeeded", "second": "succeeded"})
		if _, err := reopen().(state.StoredWorkflowLiveBaselineStore).PromoteStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", baselinePromotionRequest(suite)); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := reopen().(state.StoredWorkflowLiveBaselineRetirementStore).RetireStoredWorkflowLiveBaseline(t.Context(), app.ID, draft.Name, "production", uuid.NewString(), app.AccountID, "", api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: 1}); err != nil {
			t.Fatal("retirement requires execution target", err)
		}
	})
}
