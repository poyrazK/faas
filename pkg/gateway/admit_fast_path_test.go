package gateway_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched"
)

type countingAdmitScheduler struct {
	*gateway.FakeScheduler
	admits atomic.Int32
}

func (c *countingAdmitScheduler) AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	c.admits.Add(1)
	return c.FakeScheduler.AdmitInstance(ctx, appID, deploymentID, scope, trigger)
}

// adr: 20261123120000
// Prod 2026-11-23: an older release at 0% traffic and the live release filled
// max_concurrency 1 + the rollout grant in the gateway's picker. The gateway
// answered the next release's smoke "at capacity" without asking schedd, so
// schedd never parked the idle 0% sibling and the deploy failed as
// deployment_verification_unavailable. A smoke must reach schedd; ordinary
// traffic keeps the cheap local refusal.
func TestDeploymentSmokeAdmissionAlwaysAsksSchedd(t *testing.T) {
	for _, tc := range []struct {
		trigger        string
		wantAdmits     int32
		wantAtCapacity bool
	}{
		{trigger: sched.TriggerGateway, wantAdmits: 0, wantAtCapacity: true},
		{trigger: sched.TriggerDeploymentSmoke, wantAdmits: 1, wantAtCapacity: false},
	} {
		t.Run(tc.trigger, func(t *testing.T) {
			s := &countingAdmitScheduler{FakeScheduler: gateway.NewFakeScheduler("node-A")}
			b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, s, nil)
			b.RecordTarget("app-1", gateway.Target{NodeID: "node-A", InstanceID: "old-zero-traffic", DeploymentID: "dep-old"})
			b.RecordTarget("app-1", gateway.Target{NodeID: "node-A", InstanceID: "live", DeploymentID: "dep-live"})

			wakeID, _, atCapacity, err := b.Admit(context.Background(), "app-1", "dep-candidate", "", tc.trigger, 2)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			if got := s.admits.Load(); got != tc.wantAdmits {
				t.Fatalf("schedd AdmitInstance calls = %d, want %d", got, tc.wantAdmits)
			}
			if atCapacity != tc.wantAtCapacity {
				t.Fatalf("atCapacity = %v, want %v", atCapacity, tc.wantAtCapacity)
			}
			if !tc.wantAtCapacity && wakeID == "" {
				t.Fatal("admitted smoke returned no wake id")
			}
		})
	}
}
