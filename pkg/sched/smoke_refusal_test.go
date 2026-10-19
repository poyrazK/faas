// spec: §6.1
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A refused deployment smoke is a failed verification, not load. Feeding it
// to the pressure aggregator made the rebalancer migrate an app between nodes
// while imaged retried the smoke; an ordinary refused wake still counts.
func TestRefusedDeploymentSmokeIsNotPressure(t *testing.T) {
	cases := []struct {
		name         string
		trigger      string
		status       state.DeploymentStatus
		wantPressure int
	}{
		{name: "smoke of a failed candidate", trigger: sched.TriggerDeploymentSmoke, status: state.DeployFailed, wantPressure: 0},
		{name: "ordinary wake of a non-live candidate", trigger: sched.TriggerFloorDep, status: state.DeploySnapshotting, wantPressure: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, _ := sched.SeedApp(t, store, api.PlanPro, 256, 1)
			now := time.Now()
			agg := sched.NewPressureAggregatorForTest(time.Minute, 100, func() time.Time { return now })
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithPressureAggregator(agg)
			candidate, err := store.CreateDeployment(ctx, state.Deployment{
				AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Status: tc.status,
			})
			if err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}

			got, err := e.AdmitInstanceForDeployment(ctx, app.ID, candidate.ID, "", tc.trigger)
			if err != nil {
				t.Fatalf("AdmitInstanceForDeployment: %v", err)
			}
			if !got.AtCapacity {
				t.Fatalf("admission = %+v, want refused", got)
			}
			if pressure := agg.Count(app.ID, now); pressure != tc.wantPressure {
				t.Fatalf("pressure count = %d, want %d", pressure, tc.wantPressure)
			}
		})
	}
}
