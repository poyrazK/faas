// adr: 462
// Recovery and notifications cannot boot customer processes
// before the exact deployment's migration release has succeeded.

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrimeAndRecoveryRequireSuccessfulRelease(t *testing.T) {
	for _, status := range []string{"missing", "queued", "running", "failed", "succeeded", "wrong-command"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, app, _ := seedApp(t, store, api.PlanPro, 256, 2)
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile,
				ImageDigest: "sha256:source", Status: state.DeploySnapshotting, ReleaseCommand: []string{"/app/migrate"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/layers/release.ext4", "layers/release.ext4", 1024); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			dep, err = store.PrepareDeploymentRollback(ctx, app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status != "missing" {
				command := dep.ReleaseCommand
				if status == "wrong-command" {
					command = []string{"/app/other-migration"}
				}
				task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindRelease, Command: command})
				if err != nil {
					t.Fatal(err)
				}
				if status != "queued" {
					now := time.Now().Add(time.Second)
					claimed, err := store.ClaimNextAppTask(ctx, "test", now, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, now); err != nil {
						t.Fatal(err)
					}
					if status != "running" {
						terminal, exit := state.AppTaskSucceeded, 0
						if status == "failed" {
							terminal, exit = state.AppTaskFailed, 1
						}
						completion := state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: terminal, ExitCode: &exit, FinishedAt: now.Add(time.Second)}
						if status == "failed" {
							code, message := "migration_failed", "fixture migration failed"
							completion.FailureCode, completion.FailureMessage = &code, &message
						}
						if _, err := store.CompleteAppTask(ctx, completion); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			vmm := &fakeVMM{}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(3 * time.Minute) })
			loop.runPrimeRecovery(ctx)
			loop.waitPrimes()
			if status != "succeeded" {
				if err := engine.Prime(ctx, app.ID, dep.ID); err != nil {
					t.Fatal(err)
				}
			}
			instances, err := store.ListInstancesForApp(ctx, app.ID)
			want := 0
			if status == "succeeded" {
				want = 1
			}
			if err != nil || len(instances) != want || vmm.snapshots != want {
				t.Fatalf("release=%s instances=%d snapshots=%d err=%v; want %d", status, len(instances), vmm.snapshots, err, want)
			}
		})
	}
}
