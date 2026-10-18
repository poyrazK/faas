package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func primeObservationCandidate(t *testing.T, s state.Store, account, slug string, release []string) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	app, err := s.CreateApp(ctx, state.App{AccountID: account, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	raw, err := json.Marshal(state.StageState{Current: state.StageSnapshotPrepare, CurrentStartedAt: &at, History: []state.StageStateItem{}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeploySnapshotting, StageState: raw, ReleaseCommand: release})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/layers/test.ext4", "layers/test.ext4", 1024); err != nil {
		t.Fatal(err)
	}
	// PostgreSQL creates customer intent as pending independently of the
	// supplied status. Model the actual imaged transition before observing it.
	if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, d
}

func primeObservationInstance(t *testing.T, s state.Store, a state.App, d state.Deployment, status state.State) state.Instance {
	t.Helper()
	n, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	wake, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInstance(t.Context(), a.ID, d.ID, string(status), a.RAMMB, n.ID, wake.String())
	if err != nil {
		t.Fatal(err)
	}
	if status == state.StateParked {
		if err := s.UpdateInstanceStateWithTimestamp(t.Context(), i.ID, string(status), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	i, err = s.InstanceByID(t.Context(), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func finishObservationRelease(t *testing.T, s state.Store, a state.App, d state.Deployment, command []string) state.AppTask {
	t.Helper()
	ctx := t.Context()
	task, err := s.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: a.AccountID, AppID: a.ID, DeploymentID: d.ID, Kind: state.AppTaskKindRelease, Command: command})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextAppTask(ctx, "observation", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != task.ID {
		t.Fatal("fixture claimed another task")
	}
	if _, err := s.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	exit := 0
	task, err = s.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit, FinishedAt: time.Now().UTC().Truncate(time.Microsecond)})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// adr: 20261017122043000 — Read-only prime handoff evidence and independent collection health.
func TestSnapshotPrimeObservationStoreContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			reader := store.(state.SnapshotPrimeObservationStore)
			if got, err := reader.SnapshotPrimeSnapshot(ctx, ""); err != nil || got != (state.SnapshotPrimeSnapshot{}) {
				t.Fatalf("empty=%+v %v", got, err)
			}
			account, err := store.CreateAccount(ctx, "prime-observation@example.com", api.PlanScale)
			if err != nil {
				t.Fatal(err)
			}
			// More than recovery's four pages: observation must cover all durable rows.
			for i := range 257 {
				primeObservationCandidate(t, store, account.ID, fmt.Sprintf("wait-%d", i), nil)
			}
			activeApp, activeDep := primeObservationCandidate(t, store, account.ID, "active", nil)
			primeObservationInstance(t, store, activeApp, activeDep, state.StateColdBooting)
			parkedApp, parkedDep := primeObservationCandidate(t, store, account.ID, "parked", nil)
			parkedInstance := primeObservationInstance(t, store, parkedApp, parkedDep, state.StateRunning)
			if err := store.(state.RuntimeConfigReceiptStore).RecordInstanceRuntimeConfigReceipt(ctx, parkedInstance.ID, parkedInstance.WakeID, state.RuntimeConfigInputs{Scope: "default", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}}); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateInstanceStateWithTimestamp(ctx, parkedInstance.ID, string(state.StateParked), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			_, recorded := primeObservationCandidate(t, store, account.ID, "recorded", nil)
			if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: recorded.ID, FCVersion: "1.10.0", StorageKey: "snap/new", Tier: state.SnapshotTierInit}); err != nil {
				t.Fatal(err)
			}
			_, blocked := primeObservationCandidate(t, store, account.ID, "release-missing", []string{"/app/migrate"})
			wrongApp, wrong := primeObservationCandidate(t, store, account.ID, "release-wrong-pin", []string{"/app/migrate"})
			finishObservationRelease(t, store, wrongApp, wrong, []string{"/app/wrong"})
			releaseApp, released := primeObservationCandidate(t, store, account.ID, "release-success", []string{"/app/migrate"})
			finished := finishObservationRelease(t, store, releaseApp, released, released.ReleaseCommand)
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			for _, tc := range []struct {
				d    state.Deployment
				want bool
			}{{blocked, false}, {wrong, false}, {released, true}} {
				ok, err := engine.releaseAllowsPrime(ctx, tc.d)
				if err != nil || ok != tc.want {
					t.Fatalf("release fence=%t %v", ok, err)
				}
			}
			snap, err := reader.SnapshotPrimeSnapshot(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			for class, want := range map[state.SnapshotPrimeClass]int64{state.SnapshotPrimeWaiting: 258, state.SnapshotPrimeActive: 1, state.SnapshotPrimePublicationPending: 1, state.SnapshotPrimeRecorded: 1, state.SnapshotPrimeReleaseBlocked: 2, state.SnapshotPrimeUnknown: 0} {
				if snap[class].Pending != want {
					t.Fatalf("%s=%+v want=%d", class, snap[class], want)
				}
			}
			// Release duration never counts as eligible waiting. Isolate the row by owner.
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetAppNodeID(ctx, releaseApp.ID, node.ID); err != nil {
				t.Fatal(err)
			}
			foreign, err := reader.SnapshotPrimeSnapshot(ctx, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if foreign[state.SnapshotPrimeWaiting].Pending != 257 {
				t.Fatal("foreign owner included", foreign)
			}
			own, err := reader.SnapshotPrimeSnapshot(ctx, node.ID)
			if err != nil || own[state.SnapshotPrimeWaiting].Pending != 258 {
				t.Fatalf("owner scope=%+v %v", own, err)
			}
			if err := store.UpdateDeploymentStatus(ctx, released.ID, state.DeployCancelled, ""); err != nil {
				t.Fatal(err)
			}
			// A stale parked capture stops protecting the handoff when config changes.
			if err := store.MarkAppRuntimeConfigChanged(ctx, parkedApp.ID); err != nil {
				t.Fatal(err)
			}
			snap, err = reader.SnapshotPrimeSnapshot(ctx, "")
			if err != nil || snap[state.SnapshotPrimePublicationPending].Pending != 0 || snap[state.SnapshotPrimeWaiting].Pending != 258 {
				t.Fatalf("stale parked classification=%+v %v", snap, err)
			}
			if finished.FinishedAt == nil {
				t.Fatal("missing release completion")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := reader.SnapshotPrimeSnapshot(canceled, ""); err == nil {
				t.Fatal("canceled observation succeeded")
			}
		})
	}
}

func TestSnapshotPrimeObservationReleaseBoundaryAndRollback(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			a, err := s.CreateAccount(t.Context(), "prime-boundary@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, d := primeObservationCandidate(t, s, a.ID, "release", []string{"/app/migrate"})
			task := finishObservationRelease(t, s, app, d, d.ReleaseCommand)
			got, err := s.(state.SnapshotPrimeObservationStore).SnapshotPrimeSnapshot(t.Context(), "")
			if err != nil || task.FinishedAt == nil || !got[state.SnapshotPrimeWaiting].OldestAt.Equal(*task.FinishedAt) {
				t.Fatalf("release wait boundary=%+v task=%+v err=%v", got, task, err)
			}
			if err := s.UpdateDeploymentStatus(t.Context(), d.ID, state.DeployCancelled, ""); err != nil {
				t.Fatal(err)
			}
			app, d = primeObservationCandidate(t, s, a.ID, "rollback", nil)
			primeObservationInstance(t, s, app, d, state.StateParked)
			if _, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: d.ID, FCVersion: "1.10.0", StorageKey: "snap/old-warm", Tier: state.SnapshotTierWarm}); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentSuperseded(t.Context(), d.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			// LatestSnapshot prefers warm; a newer init capture must not let this
			// observer disagree with recovery about the earlier warm snapshot.
			if _, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: d.ID, FCVersion: "1.10.0", StorageKey: "snap/new-init", Tier: state.SnapshotTierInit}); err != nil {
				t.Fatal(err)
			}
			got, err = s.(state.SnapshotPrimeObservationStore).SnapshotPrimeSnapshot(t.Context(), "")
			if err != nil || got[state.SnapshotPrimeWaiting].Pending != 1 || got[state.SnapshotPrimeRecorded].Pending != 0 || got[state.SnapshotPrimePublicationPending].Pending != 0 {
				t.Fatalf("old rollback evidence suppressed waiting: %+v %v", got, err)
			}
		})
	}
}

func TestSnapshotPrimeObservationLegacyWakeBoundary(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, err := s.CreateAccount(t.Context(), "legacy-wake@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, d := primeObservationCandidate(t, s, account.ID, "legacy-wake", nil)
			if err := s.MarkAppRuntimeConfigChanged(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			boundary, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), s, app.ID, d.Scope)
			if err != nil {
				t.Fatal(err)
			}
			wake, err := uuid.NewV7()
			if err != nil {
				t.Fatal(err)
			}
			millis := uint64(time.Now().Add(-10 * time.Minute).UnixMilli())
			for j := range 6 {
				wake[j] = byte(millis >> uint(8*(5-j)))
			}
			node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := s.CreateInstance(t.Context(), app.ID, d.ID, string(state.StateParked), app.RAMMB, node.ID, wake.String())
			if err != nil {
				t.Fatal(err)
			}
			if !instance.StartedAt.After(boundary) {
				t.Fatal("fixture must have newer instance timestamp than invalidation")
			}
			if err := s.UpdateInstanceStateWithTimestamp(t.Context(), instance.ID, string(state.StateParked), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			engine := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			if !engine.runtimeConfigReceiptStale(t.Context(), instance, boundary, d.Scope) {
				t.Fatal("recovery oracle unexpectedly trusts stale wake")
			}
			got, err := s.(state.SnapshotPrimeObservationStore).SnapshotPrimeSnapshot(t.Context(), "")
			if err != nil || got[state.SnapshotPrimePublicationPending].Pending != 0 || got[state.SnapshotPrimeWaiting].Pending != 1 {
				t.Fatalf("older v7 wake hid stale capture: %+v %v", got, err)
			}
		})
	}
}
