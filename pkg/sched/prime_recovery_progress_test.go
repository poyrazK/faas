// adr: 191 — bounded prime recovery must progress past persistent holds.
package sched_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrimeRecoveryProgressesPastReleaseHolds(t *testing.T) {
	for _, blockers := range []int{0, 256, 512} {
		t.Run(fmt.Sprint(blockers), func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanHobby, 256, 2)
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/layers/test.ext4", "layers/test.ext4", 1024); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			dep, err := store.PrepareDeploymentRollback(ctx, app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < blockers; i++ {
				acct, err := store.CreateAccount(ctx, fmt.Sprintf("prime-hold-%d@example.test", i), api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("prime-hold-%d", i), Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: other.ID, Kind: state.DeploymentKindImage, Status: state.DeploySnapshotting, RootfsPath: "/layers/held.ext4", RootfsKey: "layers/held.ext4", ReleaseCommand: []string{"bin/migrate"}, CreatedAt: dep.CreatedAt.Add(-time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			loop := sched.NewLoop(nil, engine, sched.TestLog()).WithClock(func() time.Time { return time.Now().Add(3 * time.Minute) })
			loop.RunPrimeRecovery(ctx)
			loop.WaitPrimes()
			if blockers >= 256 {
				instances, err := store.ListInstancesForApp(ctx, app.ID)
				if err != nil || len(instances) != 0 {
					t.Fatalf("first sweep exceeded its candidate budget: instances=%+v err=%v", instances, err)
				}
			}
			for i := 0; i < 4; i++ {
				loop.RunPrimeRecovery(ctx)
				loop.WaitPrimes()
			}
			instances, err := store.ListInstancesForApp(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(instances) != 1 || instances[0].State != string(state.StateParked) {
				t.Fatalf("lost prime handoff was not recovered exactly once: %+v", instances)
			}
			// Requeue the same row after the scan has passed it. Its old parked
			// guest must not suppress a new attempt when the bounded scan wraps.
			if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.PrepareDeploymentRollback(ctx, app.ID, dep.ID); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				loop.RunPrimeRecovery(ctx)
				loop.WaitPrimes()
			}
			instances, err = store.ListInstancesForApp(ctx, app.ID)
			if err != nil || len(instances) != 2 {
				t.Fatalf("wrapped sweep did not recover the new rollback attempt exactly once: instances=%+v err=%v", instances, err)
			}
		})
	}
}
