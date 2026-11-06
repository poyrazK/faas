// adr: 191
package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func primeAttemptWorker(t *testing.T, s state.Store) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@prime-attempt.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "prime-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendDeploymentStage(ctx, d.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, d
}
func primeAttemptReplace(ctx context.Context, t *testing.T, s state.Store, app state.App) {
	t.Helper()
	next, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
}
func TestPrimeIgnoresDelayedTerminalDeployment(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, status := range []string{"snapshotting", "cancelled", "failed", "superseded"} {
				t.Run(status, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeAttemptWorker(t, s)
					var err error
					switch status {
					case "cancelled":
						_, _, err = s.CancelDeploymentTx(ctx, d.ID, "test", state.CancelReasonUser)
					case "failed":
						_, err = s.SetDeploymentFailed(ctx, d.ID, api.CodeDeployFailed, "earlier failure")
					case "superseded":
						if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
						primeAttemptReplace(ctx, t, s, app)
					}
					if err != nil {
						t.Fatal(err)
					}
					v := &sched.FakeVMM{}
					e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
					*e.WorkerReconcileSubmit() = func(context.Context, string) {}
					if err = e.Prime(ctx, app.ID, d.ID); err != nil {
						t.Fatal(err)
					}
					rows, err := s.ListInstancesForApp(ctx, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					want := 0
					if status == "snapshotting" {
						want = 1
					}
					if *v.ColdBoots() != want || len(rows) != want {
						t.Fatalf("deployment=%s boots=%d instances=%+v", status, *v.ColdBoots(), rows)
					}
				})
			}
		})
	}
}
func TestPrimeRetiresAttemptChangedDuringBoot(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, change := range []string{"superseded", "requeued"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeAttemptWorker(t, s)
					// Legacy current-live primes still use the same publication fence.
					if change == "superseded" {
						if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
					}
					v := &sched.FakeVMM{}
					e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
					*e.WorkerReconcileSubmit() = func(context.Context, string) {}
					*v.ColdBootHook() = func() {
						if change == "superseded" {
							primeAttemptReplace(ctx, t, s, app)
						} else {
							if err := s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
								t.Fatal(err)
							}
							if _, err := s.PrepareDeploymentRollback(ctx, app.ID, d.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
					err := e.Prime(ctx, app.ID, d.ID)
					if !errors.Is(err, state.ErrSnapshotPrimeStale) {
						t.Fatalf("stale prime=%v", err)
					}
					if *v.ColdBoots() != 1 || *v.Destroys() != 1 || e.Ledger().ResidentRAM() != 0 {
						t.Fatalf("stale boot leaked: boots=%d destroys=%d RAM=%d", *v.ColdBoots(), *v.Destroys(), e.Ledger().ResidentRAM())
					}
					rows, err := s.ListInstancesForApp(ctx, app.ID)
					if err != nil || len(rows) != 1 || rows[0].State != "failed" {
						t.Fatalf("stale boot published: %+v %v", rows, err)
					}
					e.MarkPrimeFailed(ctx, d.ID, state.ErrSnapshotPrimeStale)
					got, err := s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					want := state.DeploySuperseded
					if change == "requeued" {
						want = state.DeploySnapshotting
					}
					if got.Status != want {
						t.Fatalf("stale work changed replacement attempt: %+v", got)
					}
					if change == "requeued" {
						*v.ColdBootHook() = nil
						if err = e.Prime(ctx, app.ID, d.ID); err != nil {
							t.Fatalf("fresh attempt failed: %v", err)
						}
					}
				})
			}
		})
	}
}
