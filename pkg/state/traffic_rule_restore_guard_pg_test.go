//go:build !no_pg

// adr: 570

package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A restore puts back rules that fit when the version was taken but may not
// fit beside the rules written since. It passes the same guard as a create:
// the account's view for its own hosts, the global view for routes.
func TestPgTrafficRuleRestoreRefusesOverlimitHost(t *testing.T) {
	for _, tc := range []struct {
		name, scope  string
		otherAccount bool
	}{
		{"same account", "host_rule_projection", false},
		{"other account route", "global_route_rule_projection", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, account, app := trafficHostPGFixture(t)
			wide := trafficHostRule(account, app, "*")
			wide.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 259) + "1e130000]")}
			rule, err := store.CreateEdgeRule(t.Context(), wide)
			if err != nil {
				t.Fatal(err)
			}
			versions, err := store.ListEdgeRuleSetVersions(t.Context(), app.ID, 1)
			if err != nil || len(versions) == 0 {
				t.Fatalf("versions=%v err=%v", versions, err)
			}
			if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
				t.Fatal(err)
			}
			peerAccount := account
			if tc.otherAccount {
				if peerAccount, err = store.CreateAccount(t.Context(), "restore-peer@example.test", api.PlanScale); err != nil {
					t.Fatal(err)
				}
			}
			peer, err := store.CreateApp(t.Context(), App{AccountID: peerAccount.ID, Slug: "restore-peer", Status: AppActive})
			if err != nil {
				t.Fatal(err)
			}
			narrow := trafficHostRule(peerAccount, peer, "api.example.test")
			narrow.Action.Validate = wide.Action.Validate
			blocking, err := store.CreateEdgeRule(t.Context(), narrow)
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.LatestEdgeRuleChangeID(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			limits := api.MustLimitsFor(account.Plan)
			_, err = store.RestoreEdgeRuleSetVersion(t.Context(), app.ID, versions[0].Version, limits)
			var aggregate *TrafficPolicyAggregateError
			if !errors.As(err, &aggregate) || aggregate.Scope != tc.scope {
				t.Fatalf("over-limit restore accepted: %v", err)
			}
			after, err := store.LatestEdgeRuleChangeID(t.Context())
			if err != nil || after != before {
				t.Fatalf("refused restore emitted changes: before=%d after=%d err=%v", before, after, err)
			}
			if count, err := store.CountEdgeRulesForApp(t.Context(), app.ID); err != nil || count != 0 {
				t.Fatalf("refused restore changed intent: count=%d err=%v", count, err)
			}
			enabled := false
			if _, err := store.UpdateEdgeRule(t.Context(), blocking.ID, UpdateEdgeRuleParams{Enabled: &enabled}); err != nil {
				t.Fatal(err)
			}
			restored, err := store.RestoreEdgeRuleSetVersion(t.Context(), app.ID, versions[0].Version, limits)
			if err != nil || len(restored.Rules) != 1 || restored.Rules[0].ID != rule.ID {
				t.Fatalf("restore after repair: %+v err=%v", restored.Rules, err)
			}
		})
	}
}
