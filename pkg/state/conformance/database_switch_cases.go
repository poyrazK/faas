package conformance

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testDatabaseSwitches pins ADR-20261023181931's bookkeeping on both stores:
// one active switch per app, leased progress that keeps a concurrent
// cancellation request, the draining listing schedd reads, cancellation
// only before switching, and the releases a completed switch undid.
func testDatabaseSwitches(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.DatabaseSwitchStore)
	if !ok {
		t.Fatalf("%T does not implement state.DatabaseSwitchStore", fx.Store)
	}
	now := time.Now().UTC().Truncate(time.Second)
	target, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:target-" + uuid.NewString(), Status: state.DeploySuperseded,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(target): %v", err)
	}
	newSwitch := func() state.DatabaseSwitch {
		return state.DatabaseSwitch{AccountID: fx.Account.ID, AppID: fx.App.ID, Scope: "default", FromDeploymentID: fx.Deployment.ID,
			TargetDeploymentID: target.ID, UndoneDeploymentIDs: []string{fx.Deployment.ID}, SourceDatabaseID: uuid.NewString(),
			TargetName: "orders-before-v2", RestorePoint: now.Add(-time.Hour), RequestedBy: "user:conformance", CreatedAt: now}
	}
	sw, err := store.CreateDatabaseSwitch(fx.Ctx, newSwitch())
	if err != nil {
		t.Fatalf("CreateDatabaseSwitch: %v", err)
	}
	if sw.Phase != state.DatabaseSwitchRestoring || sw.ID == "" || !sw.RestorePoint.Equal(now.Add(-time.Hour)) ||
		!slices.Equal(sw.UndoneDeploymentIDs, []string{fx.Deployment.ID}) || sw.FinishedAt != nil || sw.RequestedBy != "user:conformance" {
		t.Fatalf("created = %+v", sw)
	}
	if _, err := store.CreateDatabaseSwitch(fx.Ctx, newSwitch()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second active switch err = %v, want ErrConflict", err)
	}
	if got, err := store.DatabaseSwitch(fx.Ctx, fx.Account.ID, sw.ID); err != nil || got.ID != sw.ID {
		t.Fatalf("DatabaseSwitch = %+v, %v", got, err)
	}
	if _, err := store.DatabaseSwitch(fx.Ctx, uuid.NewString(), sw.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's switch err = %v, want ErrNotFound", err)
	}

	claimed, err := store.ClaimDueDatabaseSwitches(fx.Ctx, "lease-a", now.Add(time.Second), now.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 || claimed[0].LeaseToken != "lease-a" {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, err := store.ClaimDueDatabaseSwitches(fx.Ctx, "lease-b", now.Add(2*time.Second), now.Add(time.Minute), 10); err != nil || len(again) != 0 {
		t.Fatalf("claim while leased = %+v, %v", again, err)
	}
	if _, err := store.RequestDatabaseSwitchCancel(fx.Ctx, fx.Account.ID, fx.App.ID, sw.ID, now.Add(3*time.Second)); err != nil {
		t.Fatalf("RequestDatabaseSwitchCancel: %v", err)
	}
	progress := claimed[0]
	progress.Phase, progress.TargetDatabaseID, progress.CutoverID = state.DatabaseSwitchDraining, uuid.NewString(), uuid.NewString()
	fenced := now.Add(4 * time.Second)
	progress.FencedAt, progress.DrainRequestedAt, progress.PhaseStartedAt = &fenced, &fenced, fenced
	progress.Drain, progress.RetryAt = state.DatabaseSwitchDrain{Retired: 1, Parked: 2, Pending: 3}, fenced
	saved, err := store.SaveDatabaseSwitch(fx.Ctx, progress, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("SaveDatabaseSwitch: %v", err)
	}
	if saved.Phase != state.DatabaseSwitchDraining || saved.LeaseToken != "" || saved.CancelRequestedAt == nil ||
		saved.Drain != progress.Drain || saved.FencedAt == nil || !saved.FencedAt.Equal(fenced) {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := store.SaveDatabaseSwitch(fx.Ctx, progress, now.Add(6*time.Second)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("save without the lease err = %v, want ErrConflict", err)
	}
	draining, err := store.DrainingDatabaseSwitches(fx.Ctx, 10)
	if err != nil || len(draining) != 1 || draining[0].ID != sw.ID || draining[0].CutoverID != progress.CutoverID {
		t.Fatalf("draining = %+v, %v", draining, err)
	}

	claimed, err = store.ClaimDueDatabaseSwitches(fx.Ctx, "lease-c", now.Add(7*time.Second), now.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	invalid := claimed[0]
	invalid.Phase = state.DatabaseSwitchFailed
	finished := now.Add(8 * time.Second)
	invalid.FinishedAt = &finished
	if _, err := store.SaveDatabaseSwitch(fx.Ctx, invalid, finished); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("failed without a code err = %v, want ErrInvalidArgument", err)
	}
	done := claimed[0]
	done.Phase, done.FinishedAt, done.ActivatedAt, done.ReleaseSwitchedAt = state.DatabaseSwitchCompleted, &finished, &finished, &finished
	if _, err := store.SaveDatabaseSwitch(fx.Ctx, done, finished); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := store.RequestDatabaseSwitchCancel(fx.Ctx, fx.Account.ID, fx.App.ID, sw.ID, finished); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancel after completion err = %v, want ErrConflict", err)
	}
	undone, err := store.ReleasesUndoneByDatabaseSwitches(fx.Ctx, fx.App.ID, "default")
	if err != nil || !slices.Equal(undone, []string{fx.Deployment.ID}) {
		t.Fatalf("undone = %v, %v", undone, err)
	}
	if other, err := store.ReleasesUndoneByDatabaseSwitches(fx.Ctx, fx.App.ID, "staging"); err != nil || len(other) != 0 {
		t.Fatalf("undone in another scope = %v, %v", other, err)
	}
	next, err := store.CreateDatabaseSwitch(fx.Ctx, newSwitch())
	if err != nil {
		t.Fatalf("switch after completion: %v", err)
	}
	listed, err := store.ListDatabaseSwitches(fx.Ctx, fx.Account.ID, fx.App.ID, 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	if listed[0].ID != next.ID && !listed[0].CreatedAt.Equal(listed[1].CreatedAt) {
		t.Fatalf("listing not newest first: %+v", listed)
	}
}

// testDatabaseSwitchCancelHoldAndAccountCap pins two guards: a cancel
// accepted while a leased drain step ran holds that step's advance to
// switching (due at once, so the worker aborts next), and an account runs at
// most api.DatabaseSwitchesActivePerAccount unfinished switches.
func testDatabaseSwitchCancelHoldAndAccountCap(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.DatabaseSwitchStore)
	if !ok {
		t.Fatalf("%T does not implement state.DatabaseSwitchStore", fx.Store)
	}
	now := time.Now().UTC().Truncate(time.Second)
	newSwitch := func(app state.App) state.DatabaseSwitch {
		t.Helper()
		target, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:target-" + uuid.NewString(), Status: state.DeploySuperseded,
		})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		return state.DatabaseSwitch{AccountID: fx.Account.ID, AppID: app.ID, Scope: "default", TargetDeploymentID: target.ID,
			UndoneDeploymentIDs: []string{uuid.NewString()}, SourceDatabaseID: uuid.NewString(), TargetName: "orders-before", RestorePoint: now.Add(-time.Hour),
			RequestedBy: "user:conformance", CreatedAt: now}
	}
	sw, err := store.CreateDatabaseSwitch(fx.Ctx, newSwitch(fx.App))
	if err != nil {
		t.Fatalf("CreateDatabaseSwitch: %v", err)
	}
	claimed, err := store.ClaimDueDatabaseSwitches(fx.Ctx, "lease-a", now.Add(time.Second), now.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	draining := claimed[0]
	fenced := now.Add(2 * time.Second)
	draining.Phase, draining.TargetDatabaseID, draining.CutoverID = state.DatabaseSwitchDraining, uuid.NewString(), uuid.NewString()
	draining.FencedAt, draining.DrainRequestedAt, draining.PhaseStartedAt, draining.RetryAt = &fenced, &fenced, fenced, fenced
	if _, err := store.SaveDatabaseSwitch(fx.Ctx, draining, now.Add(2*time.Second)); err != nil {
		t.Fatalf("save draining: %v", err)
	}
	claimed, err = store.ClaimDueDatabaseSwitches(fx.Ctx, "lease-b", now.Add(3*time.Second), now.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	if _, err := store.RequestDatabaseSwitchCancel(fx.Ctx, fx.Account.ID, fx.App.ID, sw.ID, now.Add(4*time.Second)); err != nil {
		t.Fatalf("cancel while the drain step runs: %v", err)
	}
	advanced := claimed[0]
	advanced.Phase, advanced.PhaseStartedAt, advanced.RetryAt = state.DatabaseSwitchSwitching, now.Add(5*time.Second), now.Add(time.Hour)
	held, err := store.SaveDatabaseSwitch(fx.Ctx, advanced, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("save the advance: %v", err)
	}
	if held.Phase != state.DatabaseSwitchDraining || !held.PhaseStartedAt.Equal(fenced) || !held.RetryAt.Equal(now.Add(5*time.Second)) || held.LeaseToken != "" {
		t.Fatalf("advance after a cancel = phase %s started %s retry %s, want held in draining and due now", held.Phase, held.PhaseStartedAt, held.RetryAt)
	}

	for i := 1; i < api.DatabaseSwitchesActivePerAccount; i++ {
		app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "switching-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		if _, err := store.CreateDatabaseSwitch(fx.Ctx, newSwitch(app)); err != nil {
			t.Fatalf("switch %d of %d: %v", i+1, api.DatabaseSwitchesActivePerAccount, err)
		}
	}
	over, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "switching-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateDatabaseSwitch(fx.Ctx, newSwitch(over)); !errors.Is(err, state.ErrDatabaseSwitchesActive) {
		t.Fatalf("switch over the account's cap err = %v, want ErrDatabaseSwitchesActive", err)
	}
}
