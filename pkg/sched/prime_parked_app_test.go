package sched_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// 2026-11-16 journeys: a release staged on a parked app failed with
// "teardown in progress". Prime boots without the app lock (deploy-path
// review P12), and the reaper's park of the evicted_cold app destroyed the
// prime's COLD_BOOTING instance mid-boot. A customer park leaves the prime to
// finish; a suspended account still loses it.
func TestParkOfAParkedAppDuringAPrimeBoot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		suspend   bool
		wantActed int
	}{
		{name: "customer park spares the prime"},
		{name: "suspended account destroys the prime", suspend: true, wantActed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mem := state.NewMemStore()
			acct, err := mem.CreateAccount(ctx, "primepark@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "primepark", RAMMB: 128, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:serving", Status: state.DeployLive}); err != nil {
				t.Fatal(err)
			}
			next, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:next", Status: state.DeploySnapshotting})
			if err != nil {
				t.Fatal(err)
			}
			if err := mem.SetDeploymentRootfs(ctx, next.ID, "/test/"+next.ID, "test/"+next.ID, 4096); err != nil {
				t.Fatal(err)
			}
			if _, err := mem.AppendDeploymentStage(ctx, next.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
				t.Fatal(err)
			}
			parked := state.AppEvictedCold
			if _, err := mem.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
				t.Fatal(err)
			}

			vmm := &sched.FakeVMM{}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			vmm.SetColdBootHook(func() {
				first := false
				once.Do(func() { first = true })
				if first {
					close(started)
					<-release
				}
			})
			e := sched.XNewEngine(t, mem, vmm, &sched.FakeNotifier{}, "1.10.0")

			primed := make(chan error, 1)
			go func() { primed <- e.Prime(ctx, app.ID, next.ID) }()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("prime never reached its boot")
			}
			if tc.suspend {
				if err := mem.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
			}

			acted, err := e.ParkApp(ctx, app.ID)
			close(release)
			primeErr := <-primed
			if err != nil {
				t.Fatalf("ParkApp: %v", err)
			}
			if acted != tc.wantActed {
				t.Fatalf("ParkApp acted on %d instances, want %d", acted, tc.wantActed)
			}
			if !tc.suspend && primeErr != nil {
				t.Fatalf("Prime of a parked app: %v", primeErr)
			}
		})
	}
}
