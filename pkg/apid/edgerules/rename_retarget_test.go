package edgerules

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// flakyRuleUpdates fails the first `failures` edge-rule updates.
type flakyRuleUpdates struct {
	MemStore *state.MemStore
	failures int
	calls    int
}

func (f *flakyRuleUpdates) UpdateEdgeRule(ctx context.Context, id string, p state.UpdateEdgeRuleParams) (state.EdgeRule, error) {
	f.calls++
	if f.calls <= f.failures {
		return state.EdgeRule{}, errors.New("transient store error")
	}
	return f.MemStore.UpdateEdgeRule(ctx, id, p)
}

type retargetHost struct {
	attackModeHost
	store state.Store
}

func (h retargetHost) Store() state.Store { return h.store }

// adr: 091 — a rename retries a failed rule move once and reports what still
// failed instead of only logging it (amendment 2026-11-03, review round 9).
func TestRetargetAfterRenameRetriesOnceAndReportsWhatFailed(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		failures int
		wantErr  bool
	}{
		"one transient failure is retried": {failures: 1},
		"a lasting failure is reported":    {failures: 100, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			mem := state.NewMemStore()
			acct, err := mem.CreateAccount(ctx, "rename-retarget@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "front", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
			if err != nil {
				t.Fatal(err)
			}
			action := state.EdgeRuleAction{Route: &state.EdgeRuleRouteAction{TargetAppSlug: "old-api"}}
			rule, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
				AccountID: acct.ID, AppID: app.ID, MatchHost: "@app", MatchPath: "/api/**", Enabled: true,
				Kind: state.EdgeRuleKindRoute, Action: action,
			})
			if err != nil {
				t.Fatal(err)
			}
			flaky := &flakyRuleUpdates{MemStore: mem, failures: tc.failures}
			svc := &Service{Host: retargetHost{attackModeHost: attackModeHost{store: mem, audits: &[]string{}}, store: statetest.NewMem(flaky)}}
			err = svc.RetargetAfterRename(ctx, acct.ID, "old-api", "new-api")
			if (err != nil) != tc.wantErr {
				t.Fatalf("RetargetAfterRename err = %v, want error %v", err, tc.wantErr)
			}
			got, err := mem.GetEdgeRuleByID(ctx, rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got.Action.Route)
			if want := !tc.wantErr; (got.Action.Route.TargetAppSlug == "new-api") != want {
				t.Fatalf("route after rename = %s, retargeted want %v", raw, want)
			}
		})
	}
}
