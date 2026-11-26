// adr: 079 — a release deployed after a worker's liveness park runs.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestNewWorkerReleaseLiftsLivenessPark pins the agent eval of 2026-11-23:
// the voting-app worker crash-looped and was parked (liveness_exhausted); the
// fixed release then went live but never ran, still read "parked: liveness
// failed", and `app restart` refused it until the agent ran `gregale wake`.
func TestNewWorkerReleaseLiftsLivenessPark(t *testing.T) {
	cases := []struct {
		name       string
		reason     state.ParkReason
		parkLive   bool // park the live release itself, not an older one
		manual     bool // the customer also ran `app park` after the liveness park
		wantActive bool
	}{
		{name: "liveness park of an older release", reason: state.ParkReasonLivenessExhausted, wantActive: true},
		{name: "liveness park of the live release", reason: state.ParkReasonLivenessExhausted, parkLive: true},
		{name: "admin park of an older release", reason: state.ParkReasonAdminPark},
		{name: "customer park after a liveness park", reason: state.ParkReasonLivenessExhausted, manual: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, live := sched.SeedApp(t, store, api.PlanPro, 256, 4)
			sched.ConfigureExecutionMode(t, store, app, live, api.ExecutionModeWorker, state.DeployLive)
			parkedID, parkedAt := live.ID, live.CreatedAt.Add(time.Minute)
			if !tc.parkLive {
				old, err := store.CreateDeployment(ctx, state.Deployment{
					AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:old", Status: state.DeploySuperseded,
				})
				if err != nil {
					t.Fatal(err)
				}
				parkedID, parkedAt = old.ID, live.CreatedAt.Add(-time.Minute)
			}
			if err := store.SetDeploymentParked(ctx, parkedID, string(tc.reason), parkedAt); err != nil {
				t.Fatal(err)
			}
			evicted := state.AppEvictedCold
			if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &evicted}); err != nil {
				t.Fatal(err)
			}
			if tc.manual {
				if _, _, err := store.BeginAppParkTransition(ctx, app.ID, state.AppActive); err != nil {
					t.Fatal(err)
				}
			}
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

			engine.ReconcileWorkerApp(ctx, app.ID)

			after, err := store.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := after.Status == state.AppActive; got != tc.wantActive {
				t.Fatalf("app status = %q; want active=%v", after.Status, tc.wantActive)
			}
			instances, err := store.ListInstancesForApp(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			running := 0
			for _, ins := range instances {
				if ins.DeploymentID == live.ID && state.State(ins.State).CountsForConcurrency() {
					running++
				}
			}
			if tc.wantActive != (running > 0) {
				t.Fatalf("live release workers = %d; want running=%v (%+v)", running, tc.wantActive, instances)
			}
		})
	}
}
