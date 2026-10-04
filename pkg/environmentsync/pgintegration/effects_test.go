package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func claimedIntentPlan(t *testing.T, store intentTestStore, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) environmentsync.Plan {
	t.Helper()
	observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
		Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation,
		Prune: lease.Source.Spec.Prune, Now: time.Now(), Overrides: observed.Overrides,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestEnvironmentGitOpsEffectsCommitWithIntentAndFenceConvergence(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		effects := basic.(state.EnvironmentGitOpsEffectStore)
		source, desired, app := intentFixture(t, store, "enforce")
		adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash) != nil {
			t.Fatal("could not adopt fixture", err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "effect-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		for _, invalid := range [][]state.EnvironmentGitOpsEffectSpec{
			{},
			{{AppID: app.ID, Kind: "edge_policy", GatewayGeneration: 42}},
			{{AppID: app.ID, Kind: "edge_policy", GatewayGeneration: 0, MatchHosts: []string{"api.example.test"}}},
		} {
			if _, err := effects.ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, invalid); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("missing or invalid effects accepted: %v", err)
			}
			assertVariable(t, basic, source, app, "production", "MODE", "console")
			pending, err := effects.PendingEnvironmentGitOpsEffects(t.Context(), lease)
			if err != nil || len(pending) != 0 {
				t.Fatalf("rejected plan leaked effects: %+v %v", pending, err)
			}
		}
		spec := state.EnvironmentGitOpsEffectSpec{AppID: app.ID, Kind: "edge_policy", GatewayGeneration: 42,
			MatchHosts: []string{"api.example.test"}, ExpectedNodes: []string{"node-b", "node-a", "node-a"}}
		steps, err := effects.ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, []state.EnvironmentGitOpsEffectSpec{spec})
		if err != nil || len(steps) == 0 {
			t.Fatalf("intent and effects commit: %+v %v", steps, err)
		}
		assertVariable(t, basic, source, app, "production", "MODE", "production")
		pending, err := effects.PendingEnvironmentGitOpsEffects(t.Context(), lease)
		if err != nil || len(pending) != 1 || pending[0].PlanHash != plan.Hash || !reflect.DeepEqual(pending[0].ExpectedNodes, []string{"node-a", "node-b"}) {
			t.Fatalf("durable effect: %+v %v", pending, err)
		}
		effect := pending[0]
		verified := claimedIntentPlan(t, store, lease, desired)
		rawPlan, _ := json.Marshal(verified)
		now := time.Now()
		finish := func() error {
			return store.FinishEnvironmentGitOps(t.Context(), lease, "converged", rawPlan, json.RawMessage(`[]`), "", now, now.Add(time.Minute))
		}
		if verified.HasDrift() || !verified.CanApply() || !errors.Is(finish(), state.ErrConflict) {
			t.Fatal("equal intent hid an unacknowledged fleet effect")
		}
		for _, ack := range []struct {
			generation int64
			node       string
		}{{41, "node-a"}, {42, "unknown"}} {
			if err := effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), lease, effect.ID, ack.generation, ack.node); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unrelated acknowledgement accepted: %v", err)
			}
		}
		if err := effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), lease, effect.ID, 42, "node-a"); err != nil {
			t.Fatal(err)
		}
		if err := effects.CompleteEnvironmentGitOpsEffect(t.Context(), lease, effect.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("one of two gateways completed effect: %v", err)
		}
		extended, err := effects.ExtendEnvironmentGitOpsEffectTargets(t.Context(), lease, effect.ID, []string{"node-c"})
		if err != nil || !reflect.DeepEqual(extended.ExpectedNodes, []string{"node-a", "node-b", "node-c"}) {
			t.Fatalf("recovery dropped the original required fleet: %+v %v", extended, err)
		}
		for _, node := range []string{"node-b", "node-c"} {
			if err := effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), lease, effect.ID, 42, node); err != nil {
				t.Fatal(err)
			}
		}
		if err := effects.CompleteEnvironmentGitOpsEffect(t.Context(), lease, effect.ID); err != nil {
			t.Fatal(err)
		}
		finishColdGitOpsRuntime(t, basic, lease)
		if err := finish(); err != nil {
			t.Fatalf("fully acknowledged effect prevented finish: %v", err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		var persisted []state.EnvironmentGitOpsStep
		if err != nil || len(runs) != 1 || json.Unmarshal(runs[0].Steps, &persisted) != nil || len(persisted) != len(steps) {
			t.Fatalf("finish discarded committed progress after an empty reply: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsEffectsSurviveSupersessionAndRejectOldWorker(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-generation-after-expiry", true: "new-generation"}[supersede], func(t *testing.T) {
			testGitOpsEffectsReplacementWorker(t, supersede)
		})
	}
}

func testGitOpsEffectsReplacementWorker(t *testing.T, supersede bool) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		effects := basic.(state.EnvironmentGitOpsEffectStore)
		source, desired, app := intentFixture(t, store, "enforce")
		adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash); err != nil {
			t.Fatal(err)
		}
		old, err := store.ClaimEnvironmentGitOps(t.Context(), "pre-crash-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, old, desired)
		if _, err := effects.ApplyEnvironmentGitOpsWithEffects(t.Context(), old, plan, []state.EnvironmentGitOpsEffectSpec{{AppID: app.ID,
			Kind: "edge_policy", GatewayGeneration: 42, MatchHosts: []string{"api.example.test"}, ExpectedNodes: []string{"node-a"}}}); err != nil {
			t.Fatal(err)
		}
		claimAt := time.Now()
		if supersede {
			prune := true
			if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune}); err != nil {
				t.Fatal(err)
			}
			claimAt = time.Now()
		} else {
			claimAt = claimAt.Add(2 * time.Minute)
		}
		fresh, err := store.ClaimEnvironmentGitOps(t.Context(), "restarted-worker", claimAt, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := effects.PendingEnvironmentGitOpsEffects(t.Context(), fresh)
		if err != nil || len(pending) != 1 || pending[0].Generation != old.Source.Generation {
			t.Fatalf("supersession discarded post-commit work: %+v %v", pending, err)
		}
		id := pending[0].ID
		if _, err := effects.PendingEnvironmentGitOpsEffects(t.Context(), old); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded worker observed with old authority: %v", err)
		}
		if err := effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), old, id, 42, "node-a"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded worker completed effect: %v", err)
		}
		if err := effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), fresh, id, 42, "node-a"); err != nil {
			t.Fatal(err)
		}
		if err := effects.CompleteEnvironmentGitOpsEffect(t.Context(), fresh, id); err != nil {
			t.Fatal(err)
		}
		finishColdGitOpsRuntime(t, basic, fresh)
		verified := claimedIntentPlan(t, store, fresh, desired)
		rawPlan, _ := json.Marshal(verified)
		now := claimAt
		if err := store.FinishEnvironmentGitOps(t.Context(), fresh, "converged", rawPlan, json.RawMessage(`[]`), "", now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	})
}
