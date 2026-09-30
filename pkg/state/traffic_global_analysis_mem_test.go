// adr: 375
package state

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemTrafficGlobalRouteConcurrentAccountsShareAllowance(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	peerAccount, err := m.CreateAccount(t.Context(), "mem-global-peer@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := m.CreateApp(t.Context(), App{AccountID: peerAccount.ID, Slug: "mem-global-peer"})
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, in := range []CreateEdgeRuleParams{memTrafficRule(account, app, "*", 260), memTrafficRule(peerAccount, peer, "*", 260)} {
		go func() {
			<-start
			_, err := m.CreateEdgeRuleIfUnderQuota(t.Context(), in, api.MustLimitsFor(account.Plan))
			results <- err
		}()
	}
	close(start)
	accepted := 0
	for range 2 {
		if err := <-results; err != nil {
			requireMemTrafficAggregate(t, err, "global_route_rule_projection")
		} else {
			accepted++
		}
	}
	if accepted != 1 || len(m.edgeRules) != 1 {
		t.Fatalf("global allowance: accepted=%d rows=%d", accepted, len(m.edgeRules))
	}
}

func TestMemTrafficGlobalDisjointHostsAndEnableRollback(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	peerAccount, err := m.CreateAccount(t.Context(), "mem-global-disjoint@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := m.CreateApp(t.Context(), App{AccountID: peerAccount.ID, Slug: "mem-global-disjoint"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateEdgeRule(t.Context(), memTrafficRule(account, app, "api.*", 260)); err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateEdgeRule(t.Context(), memTrafficRule(peerAccount, peer, "web.*", 260))
	if err != nil {
		t.Fatalf("disjoint hosts became a global quota: %v", err)
	}
	host := "*.test"
	_, err = m.UpdateEdgeRule(t.Context(), second.ID, UpdateEdgeRuleParams{MatchHost: &host})
	requireMemTrafficAggregate(t, err, "global_route_rule_projection")
	if saved := m.edgeRules[second.ID]; saved.MatchHost != second.MatchHost || !saved.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatal("rejected global retarget changed intent")
	}
	in := memTrafficRule(peerAccount, peer, "api.*", 260)
	in.Enabled = false
	disabled, err := m.CreateEdgeRule(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	_, err = m.UpdateEdgeRule(t.Context(), disabled.ID, UpdateEdgeRuleParams{Enabled: &enabled})
	requireMemTrafficAggregate(t, err, "global_route_rule_projection")
	if m.edgeRules[disabled.ID].Enabled {
		t.Fatal("rejected global enable persisted")
	}
}

func TestGlobalTrafficErrorScopesRetainTypedContract(t *testing.T) {
	for _, err := range []error{
		&TrafficPolicyAggregateError{Scope: "host_rule_count", Unit: "rules", Limit: 2, Observed: 3, Host: "example.test"},
		&TrafficPolicyAggregateError{Scope: "host_compiled_projection_estimate", Unit: "bytes", Limit: 2, Observed: 3, Host: "example.test"},
		&TrafficPolicyAnalysisError{Scope: "database_time", Unit: "milliseconds", Limit: 2, Observed: 3},
	} {
		wrapped := globalTrafficPolicyError(err)
		var aggregate *TrafficPolicyAggregateError
		var analysis *TrafficPolicyAnalysisError
		if errors.As(wrapped, &aggregate) {
			if !strings.HasPrefix(aggregate.Scope, "global_route_") || aggregate.Host != "example.test" || aggregate.Observed != 3 {
				t.Fatal("global aggregate lost typed evidence")
			}
		} else if !errors.As(wrapped, &analysis) || analysis.Scope != "global_route_database_time" || analysis.Observed != 3 {
			t.Fatal("global analysis lost typed evidence")
		}
	}
}
