// adr: 570
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func largeTrafficRuleAction() state.EdgeRuleAction {
	// Small wire JSON expands beyond 64 MiB in PostgreSQL numeric notation.
	// Inactive union members remain part of the runtime SQL projection.
	return state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute,
		Route:    &state.EdgeRuleRouteAction{TargetAppSlug: "projection-api"},
		Validate: &state.EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 529) + "1e130000]")}}
}

func requireTrafficRuleProjectionError(t *testing.T, err error) {
	t.Helper()
	var projection *state.TrafficPolicyProjectionError
	if !errors.As(err, &projection) || projection.Scope != "edge_rule" ||
		projection.Limit != api.TrafficPolicyMaxHostBytes || projection.Observed <= projection.Limit {
		t.Fatalf("expected edge rule runtime bound, got %v", err)
	}
}

func trafficRuleProjectionWriteRecovery(t *testing.T, store state.Store) {
	t.Helper()
	account, _, app := trafficProjectionOwner(t, store)
	ctx := context.Background()
	in := state.CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID,
		MatchHost: "rule-projection.example.test", MatchPath: "/", Enabled: true,
		Kind: state.EdgeRuleKindRoute, Action: largeTrafficRuleAction()}
	for _, quota := range []bool{false, true} {
		var err error
		if quota {
			_, err = store.CreateEdgeRuleIfUnderQuota(ctx, in, api.MustLimitsFor(account.Plan))
		} else {
			_, err = store.CreateEdgeRule(ctx, in)
		}
		requireTrafficRuleProjectionError(t, err)
		if count, err := store.CountEdgeRulesForApp(ctx, app.ID); err != nil || count != 0 {
			t.Fatalf("rejected create persisted: count=%d err=%v", count, err)
		}
	}
	in.Action.Validate = nil
	rule, err := store.CreateEdgeRule(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	large, priority := largeTrafficRuleAction(), 10
	_, err = store.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Action: &large, Priority: &priority})
	requireTrafficRuleProjectionError(t, err)
	saved, err := store.GetEdgeRuleByID(ctx, rule.ID)
	if err != nil || saved.Priority != rule.Priority || saved.Action.Validate != nil || !saved.UpdatedAt.Equal(rule.UpdatedAt) {
		t.Fatalf("rejected replacement changed intent: saved=%+v err=%v", saved, err)
	}
	in.Action.Route.TargetAppSlug = "repaired"
	if _, err := store.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Action: &in.Action}); err != nil {
		t.Fatalf("smaller replacement failed: %v", err)
	}
	if err := store.DeleteEdgeRule(ctx, rule.ID); err != nil {
		t.Fatalf("delete recovery: %v", err)
	}
}

func TestMemTrafficEdgeRuleProjectionWriteRecovery(t *testing.T) {
	trafficRuleProjectionWriteRecovery(t, state.NewMemStore())
}
