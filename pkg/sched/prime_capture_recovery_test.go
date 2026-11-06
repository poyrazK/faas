package sched_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestPrimeRecoveryResumesRunningCapture(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := primeHandoffStore(t, backend)
			for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeService} {
				t.Run(mode, func(t *testing.T) {
					for _, change := range []string{"current", "direct-replay", "environment", "cancelled", "queue-failure", "capture-failure", "capture-barrier"} {
						t.Run(change, func(t *testing.T) {
							ctx := t.Context()
							app, d := primeHandoffFixture(t, s, mode)
							v, n := &primeHandoffVM{}, &primeHandoffNotifier{crashRunning: true}
							crashPrimeHandoff(t, primeHandoffEngine(t, s, v, n), app.ID, d.ID)
							rows, err := s.ListInstancesForApp(ctx, app.ID)
							if err != nil || len(rows) != 1 || rows[0].State != "running" {
								t.Fatalf("crash not durable: %+v %v", rows, err)
							}
							old := rows[0]
							n.crashRunning = false
							switch change {
							case "environment":
								if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new"); err != nil {
									t.Fatal(err)
								}
							case "cancelled":
								if _, _, err := s.CancelDeploymentTx(ctx, d.ID, "test", state.CancelReasonUser); err != nil {
									t.Fatal(err)
								}
							case "queue-failure":
								n.failSnapshot = true
							case "capture-failure":
								v.captureErr = errors.New("snapshot capture failed")
							case "capture-barrier":
								if err := s.(state.ExclusiveSnapshotStore).BeginExclusiveSnapshot(ctx, old.ID); err != nil {
									t.Fatal(err)
								}
							}
							restarted := primeHandoffEngine(t, s, v, n)
							loop := sched.NewLoop(nil, restarted, primeHandoffLog())
							if change == "direct-replay" {
								if err := restarted.Prime(ctx, app.ID, d.ID); err != nil {
									t.Fatal(err)
								}
							}
							for pass := range 3 {
								if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Hour)); err != nil {
									t.Fatal(err)
								}
								loop.WaitPrimes()
								n.failSnapshot = false
								if change == "queue-failure" && pass == 0 {
									if got, err := s.DeploymentByID(ctx, d.ID); err != nil || got.Status != state.DeploySnapshotting {
										t.Fatalf("queue failure consumed deployment: %+v %v", got, err)
									}
								}
							}
							current, err := s.InstanceByID(ctx, old.ID)
							if err != nil {
								t.Fatal(err)
							}
							if change == "cancelled" {
								if v.boots != 1 || v.captures != 0 || n.snapshots != 0 {
									t.Fatalf("cancelled prime resumed: boots=%d captures=%d messages=%d", v.boots, v.captures, n.snapshots)
								}
								return
							}
							if change == "capture-failure" {
								got, err := s.DeploymentByID(ctx, d.ID)
								if err != nil || got.Status != state.DeployFailed || current.State != "stopped" || v.boots != 1 || v.captures != 1 || n.snapshots != 0 {
									t.Fatalf("capture failure lost normal prime failure semantics: deployment=%s instance=%s boots=%d captures=%d messages=%d err=%v", got.Status, current.State, v.boots, v.captures, n.snapshots, err)
								}
								return
							}
							wantBoots := 1
							wantOldState := "parked"
							if change == "environment" {
								wantBoots = 2
								wantOldState = "stopped"
							}
							if v.boots != wantBoots || v.captures != 1 || n.snapshots == 0 || current.State != wantOldState || len(restarted.Ledger().InstancesForApp(app.ID)) != 0 {
								t.Fatalf("prime did not recover exactly once: boots=%d captures=%d messages=%d old=%s RAM=%d", v.boots, v.captures, n.snapshots, current.State, restarted.Ledger().ResidentRAM())
							}
						})
					}
				})
			}
		})
	}
}

func TestPrimeCaptureRecoveryInputFailurePreservesGuest(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	app, d := primeHandoffFixture(t, s, api.ExecutionModeService)
	v, n := &primeHandoffVM{}, &primeHandoffNotifier{crashRunning: true}
	crashPrimeHandoff(t, primeHandoffEngine(t, s, v, n), app.ID, d.ID)
	n.crashRunning = false
	failing := statetest.NewMem(&failingPrimeRecoveryInputs{MemStore: s})
	restarted := primeHandoffEngine(t, failing, v, n)
	if err := restarted.Prime(ctx, app.ID, d.ID); err == nil {
		t.Fatal("unavailable inputs were accepted")
	}
	rows, err := s.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].State != "running" || v.destroys != 0 || v.captures != 0 || restarted.Ledger().ResidentRAM() == 0 {
		t.Fatalf("uncertain inputs changed runtime: %+v err=%v captures=%d destroys=%d", rows, err, v.captures, v.destroys)
	}
	if err := primeHandoffEngine(t, s, v, n).Prime(ctx, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if v.boots != 1 || v.captures != 1 || n.snapshots != 1 {
		t.Fatal("recovered inputs did not resume the original VM")
	}
}

func TestPooledSchedulerPrimesKeepSessionHeadroom(t *testing.T) {
	migrated := pgtest.OpenMigrated(t)
	// Prepare intent on the fixture's warm pool before starting the daemon
	// pools/listener. This test's deadline covers session contention and boot.
	app, d := primeHandoffFixture(t, state.NewPgStore(migrated), api.ExecutionModeWorker)
	parsed, err := url.Parse(migrated.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg := migrated.Config().ConnConfig
	parsed.Path = "/" + cfg.Database
	query := parsed.Query()
	for k, v := range cfg.RuntimeParams {
		query.Set(k, v)
	}
	parsed.RawQuery = query.Encode()
	dsn := parsed.String()
	t.Setenv(db.DirectDSNEnv, dsn)
	t.Setenv(db.NotifyHubEnv, "1")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, err := db.OpenWithAppName(ctx, dsn, "faas-schedd")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(pool)
	listenCtx, stopListener := context.WithCancel(ctx)
	defer stopListener()
	if _, err := db.SubscribeWithResync(listenCtx, pool, []string{db.NotifySnapshotPrime}, primeHandoffLog()); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	// Retain the same four sessions as the background prime workers.
	for range api.SnapshotPrimeConcurrentSlots {
		release, err := s.AcquireSnapshotPrimeLock(ctx, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		defer release(context.WithoutCancel(ctx))
	}
	entered, unblock := make(chan struct{}), make(chan struct{})
	var released bool
	releaseBoot := func() {
		if !released {
			close(unblock)
			released = true
		}
	}
	defer releaseBoot()
	v, n := &primeHandoffVM{}, &primeHandoffNotifier{}
	v.bootHook = func() { close(entered); <-unblock }
	e := primeHandoffEngine(t, s, v, n)
	done := make(chan error, 1)
	go func() { done <- e.Prime(ctx, app.ID, d.ID) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("overflow prime could not boot: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The fifth (inline) prime retains its own lock while one extra direct
	// connection remains usable for a short traffic/lifecycle operation.
	conn, err := db.DirectPool(pool).Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Ping(ctx)
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	releaseBoot()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if v.boots != 1 || n.ready != 1 {
		t.Fatalf("unrelated prime did not complete: boots=%d ready=%d", v.boots, n.ready)
	}
}
