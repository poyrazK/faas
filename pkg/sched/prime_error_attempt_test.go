// adr: 191
package sched_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrimeFailureRetainsAttemptOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, change := range []string{"unchanged", "superseded", "requeued", "watchdog"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeAttemptWorker(t, s)
					v := &sched.FakeVMM{}
					cause := errors.New("cold boot failed")
					var bootErr error = cause
					if change == "watchdog" {
						bootErr = api.NewProblem(422, api.CodeAppStartupTimeout, "not ready", "startup_phase=handler_healthcheck: last status 503")
					}
					*v.WakeErr() = bootErr
					*v.ColdBootHook() = func() {
						if change == "watchdog" {
							if _, err := s.SetDeploymentFailedEx(ctx, d.ID, api.CodeAppStartupTimeout, "cold_boot_timeout: instance=test", "", "", "", nil); err != nil {
								t.Fatal(err)
							}
							return
						}
						if change == "unchanged" {
							return
						}
						if err := s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
						if change == "requeued" {
							if _, err := s.PrepareDeploymentRollback(ctx, app.ID, d.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
					e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
					err := e.Prime(ctx, app.ID, d.ID)
					if err == nil || !errors.Is(err, bootErr) {
						t.Fatalf("startup error was not preserved: %v", err)
					}
					e.MarkPrimeFailed(ctx, d.ID, err)
					got, err := s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					want := state.DeployFailed
					if change == "superseded" {
						want = state.DeploySuperseded
					}
					if change == "requeued" {
						want = state.DeploySnapshotting
					}
					if got.Status != want {
						t.Fatalf("failure crossed attempts: status=%s want=%s stage=%s", got.Status, want, got.StageState)
					}
					if change == "watchdog" && !strings.Contains(got.Error, "last status 503") {
						t.Fatalf("same-attempt watchdog diagnostic was lost: %s", got.Error)
					}
				})
			}
		})
	}
}

func TestPrimeReplayAfterCompletedReadinessDoesNotBoot(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			ctx := t.Context()
			app, d := primeAttemptWorker(t, s)
			v := &sched.FakeVMM{}
			e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
			if err := e.Prime(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendDeploymentStage(ctx, d.ID, state.StageSnapshotPrepare, state.StageReadiness, time.Now().UTC(), ""); err != nil {
				t.Fatal(err)
			}
			if err := e.Prime(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendDeploymentStage(ctx, d.ID, state.StageReadiness, "", time.Now().UTC(), ""); err != nil {
				t.Fatal(err)
			}
			if err := e.Prime(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			if *v.ColdBoots() != 1 {
				t.Fatalf("completed prime replay executed another boot: %d", *v.ColdBoots())
			}
			if err := s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PrepareDeploymentRollback(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := e.Prime(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			if *v.ColdBoots() != 2 {
				t.Fatalf("explicit rollback did not authorize a fresh prime: %d", *v.ColdBoots())
			}
		})
	}
}
