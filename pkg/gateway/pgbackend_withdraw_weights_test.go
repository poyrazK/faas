package gateway_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// ADR-1151: the reaper can freeze an app's only instance while a request on
// this gateway is already waiting for a wake. The frozen notification
// withdraws the route; the thaw that request's wake returns, under the same
// instance ID, must then be routable at once. Production-us 2026-10-21
// (h10-fn-go): the withdrawal dropped the app's picker with its deployment
// weights, the thawed target landed in a picker without weights, and the
// waiting request saw no healthy target until it 503ed after 29.8 s.
func TestPGBackend_TargetRecordedAfterTheLastRemovalIsRoutable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(b *gateway.PGBackend)
		// next is the target a wake records after the removal.
		next gateway.Target
	}{
		{name: "frozen instance withdrawn, then thawed", remove: func(b *gateway.PGBackend) { b.WithdrawInstance("app-1", "i-1") },
			next: gateway.Target{InstanceID: "i-1", NodeID: "node-a", DeploymentID: "dep-1"}},
		{name: "stopped instance evicted, then a new one restored", remove: func(b *gateway.PGBackend) { b.EvictInstance("app-1", "i-1") },
			next: gateway.Target{InstanceID: "i-2", NodeID: "node-a", DeploymentID: "dep-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler("node-a"), nil).
				WithStore(&fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{
					"app-1": {{ID: "dep-1", TrafficPercent: 100}},
				}})
			// The request's route preparation read the weights before it
			// found no target and started the wake.
			if err := b.RefreshDeploymentWeights(context.Background(), "app-1"); err != nil {
				t.Fatalf("RefreshDeploymentWeights: %v", err)
			}
			b.RecordTarget("app-1", gateway.Target{InstanceID: "i-1", NodeID: "node-a", DeploymentID: "dep-1"})
			tc.remove(b)
			if got := b.HealthyCount("app-1"); got != 0 {
				t.Fatalf("HealthyCount after the removal = %d, want 0", got)
			}
			b.RecordTarget("app-1", tc.next)
			if got := b.HealthyCount("app-1"); got != 1 {
				t.Fatalf("HealthyCount after the wake recorded %s = %d, want 1", tc.next.InstanceID, got)
			}
			if pick := b.Pick("app-1"); !pick.OK || pick.Target.InstanceID != tc.next.InstanceID {
				t.Fatalf("Pick = %+v ok=%v, want %s", pick.Target, pick.OK, tc.next.InstanceID)
			}
		})
	}
}
