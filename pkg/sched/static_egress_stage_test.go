// adr: 119
package sched_test

import (
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the production lane carries an app's static egress IP; a stage leaves
// through the shared egress pool so a partner allowlist for production's
// address never accepts stage traffic.
func TestDeploymentStaticEgressIPProductionLaneOnly(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.7")
	app := state.App{StaticEgressIP: &ip}
	for _, tc := range []struct {
		scope, want string
	}{
		{"", "203.0.113.7"},
		{state.DefaultEnvScope, "203.0.113.7"},
		{"production", "203.0.113.7"},
		{"staging", ""},
		{"pr-12", ""},
	} {
		if got := sched.DeploymentStaticEgressIP(app, state.Deployment{Scope: tc.scope}); got != tc.want {
			t.Errorf("scope %q: static egress IP = %q, want %q", tc.scope, got, tc.want)
		}
	}
	if got := sched.DeploymentStaticEgressIP(state.App{}, state.Deployment{Scope: "production"}); got != "" {
		t.Errorf("unpinned app got static egress IP %q", got)
	}
}

func TestStaticEgressWireRetainsProductionAndStagePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, pin string
		excluded  bool
	}{{"production", "203.0.113.7", false}, {"stage", "", true}, {"legacy", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			pin, excluded := sched.StaticEgressWirePolicyForTest(sched.AppSpec{StaticEgressIP: tc.pin, StaticEgressExcluded: tc.excluded})
			if pin != tc.pin || excluded != tc.excluded {
				t.Fatalf("wire lost egress eligibility: pin=%q excluded=%t", pin, excluded)
			}
		})
	}
}
