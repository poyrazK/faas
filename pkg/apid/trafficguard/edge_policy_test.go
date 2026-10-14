package trafficguard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type edgeLedger struct {
	revision int64
	err      error
}

func (s edgeLedger) LatestEdgeRuleChangeID(context.Context) (int64, error) { return s.revision, s.err }
func (s edgeLedger) LatestAppEdgeRuleChangeID(context.Context, string) (int64, error) {
	return 17, s.err
}
func (s edgeLedger) LatestAccountEdgeRulePolicyPosition(context.Context, string) (state.EdgeRulePolicyPosition, error) {
	return state.EdgeRulePolicyPosition{DesiredRevision: s.revision, LegacyFleetRevision: 9}, s.err
}
func TestEdgePolicyScopeAndRevision(t *testing.T) {
	for _, raw := range []string{"", "app", "account", "fleet"} {
		if _, err := EdgePolicyScope(raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EdgePolicyScope("invalid"); err == nil {
		t.Fatal("invalid scope accepted")
	}
	for _, scope := range []string{"app", "account", "fleet"} {
		position, err := EdgePolicyPosition(t.Context(), edgeLedger{revision: 42}, scope, "app-id", "account-id")
		want := int64(42)
		if scope == "app" {
			want = 17
		}
		if err != nil || position.DesiredRevision != want {
			t.Fatalf("%s: %+v %v", scope, position, err)
		}
		if scope == "account" && position.LegacyFleetRevision != 9 {
			t.Fatal("legacy floor lost")
		}
		if _, err := EdgePolicyPosition(t.Context(), nil, scope, "app-id", "account-id"); err == nil {
			t.Fatal("missing ledger accepted")
		}
		if _, err := EdgePolicyPosition(t.Context(), edgeLedger{err: errors.New("unavailable")}, scope, "app-id", "account-id"); err == nil {
			t.Fatal("failed ledger accepted")
		}
	}
}
func TestFleetEdgePolicyFreshness(t *testing.T) {
	now := time.Now()
	for _, scenario := range []string{"fresh", "behind", "stale", "missing", "no-revision"} {
		t.Run(scenario, func(t *testing.T) {
			gateways := []state.ServingGatewayControlPlaneState{{}}
			desired := int64(42)
			revision := int64(42)
			observed := now
			switch scenario {
			case "behind":
				revision = 41
			case "stale":
				observed = now.Add(-11 * time.Second)
			case "missing":
				gateways = nil
			case "no-revision":
				desired = 0
			}
			status := SummarizeRuntimePolicyComponent("fleet", desired, gateways, now, 10*time.Second, func(state.ServingGatewayControlPlaneState) int64 { return revision }, func(state.ServingGatewayControlPlaneState) time.Time { return observed })
			if (status.State == "active") != (scenario == "fresh") {
				t.Fatalf("%+v", status)
			}
			if scenario == "stale" && status.StaleGateways != 1 {
				t.Fatal("stale evidence accepted")
			}
		})
	}
}
