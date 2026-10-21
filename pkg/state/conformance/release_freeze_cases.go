package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testReleaseFreezes pins ADR-20261019071733's bookkeeping on both stores:
// the per-account limit counts only unlifted freezes that have not ended,
// ended windows drop out of the list, an automatic freeze is created once per
// rolled-back deployment, and lifting is final.
func testReleaseFreezes(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.ReleaseFreezeStore)
	if !ok {
		t.Fatalf("%T does not implement state.ReleaseFreezeStore", fx.Store)
	}
	acct, app := fx.Account.ID, fx.App.ID
	now := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	window, err := store.CreateReleaseFreeze(fx.Ctx, state.ReleaseFreeze{AccountID: acct, AppID: app,
		Kind: state.ReleaseFreezeWindow, StartsAt: at(-time.Hour), EndsAt: at(time.Hour),
		Reason: "holiday", CreatedBy: "user:conformance"}, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if window.Source != state.ReleaseFreezeSourceManual || window.AppID != app || window.EndsAt == nil || !window.EndsAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("window freeze = %+v", window)
	}
	weekly, err := store.CreateReleaseFreeze(fx.Ctx, state.ReleaseFreeze{AccountID: acct, Kind: state.ReleaseFreezeWeekly,
		WeeklyDays: 1 << int(time.Friday), WeeklyStartMinute: 16 * 60, WeeklyDurationMinutes: 8 * 60, TimeZone: "Europe/Istanbul",
		Reason: "no Friday evening releases", CreatedBy: "user:conformance"}, 3, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if weekly.AppID != "" || weekly.WeeklyDays != 1<<int(time.Friday) || weekly.WeeklyStartMinute != 960 || weekly.WeeklyDurationMinutes != 480 || weekly.TimeZone != "Europe/Istanbul" {
		t.Fatalf("weekly freeze = %+v", weekly)
	}
	policy, err := store.CreateReleaseFreeze(fx.Ctx, state.ReleaseFreeze{AccountID: acct, AppID: app, Kind: state.ReleaseFreezeAfterRollback,
		AfterRollbackMinutes: 30, Reason: "settle after a rollback", CreatedBy: "user:conformance"}, 3, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if policy.AfterRollbackMinutes != 30 || policy.StartsAt != nil {
		t.Fatalf("after-rollback policy = %+v", policy)
	}
	if _, err := store.CreateReleaseFreeze(fx.Ctx, state.ReleaseFreeze{AccountID: acct, Kind: state.ReleaseFreezeWindow,
		StartsAt: at(0), Reason: "one too many", CreatedBy: "user:conformance"}, 3, now.Add(3*time.Second)); !errors.Is(err, state.ErrReleaseFreezeLimit) {
		t.Fatalf("fourth freeze err = %v, want ErrReleaseFreezeLimit", err)
	}

	// Two hours on, the window has ended: it leaves the list and frees a slot.
	later := now.Add(2 * time.Hour)
	listed, err := store.ListReleaseFreezes(fx.Ctx, acct, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != weekly.ID || listed[1].ID != policy.ID {
		t.Fatalf("listed after window ended = %+v", listed)
	}
	if _, err := store.CreateReleaseFreeze(fx.Ctx, state.ReleaseFreeze{AccountID: acct, Kind: state.ReleaseFreezeWindow,
		StartsAt: &later, Reason: "open-ended", CreatedBy: "user:conformance"}, 3, later); err != nil {
		t.Fatalf("freeze after a window ended: %v", err)
	}

	// An automatic freeze is created once per rolled-back deployment.
	rolledBack := uuid.NewString()
	automatic := state.ReleaseFreeze{AccountID: acct, AppID: app, StartsAt: &later, EndsAt: at(3 * time.Hour),
		Reason: "automatic rollback of v7", SourceDeploymentID: rolledBack, CreatedBy: "gregale"}
	created, err := store.CreateRollbackReleaseFreeze(fx.Ctx, automatic)
	if err != nil || !created {
		t.Fatalf("first automatic freeze created=%v err=%v", created, err)
	}
	if created, err := store.CreateRollbackReleaseFreeze(fx.Ctx, automatic); err != nil || created {
		t.Fatalf("repeated automatic freeze created=%v err=%v", created, err)
	}
	listed, err = store.ListReleaseFreezes(fx.Ctx, acct, later)
	if err != nil {
		t.Fatal(err)
	}
	var auto state.ReleaseFreeze
	for _, f := range listed {
		if f.Source == state.ReleaseFreezeSourceAutomaticRollback {
			auto = f
		}
	}
	if auto.ID == "" || auto.Kind != state.ReleaseFreezeWindow || auto.SourceDeploymentID != rolledBack || auto.AppID != app {
		t.Fatalf("automatic freeze = %+v in %+v", auto, listed)
	}

	lifted, err := store.LiftReleaseFreeze(fx.Ctx, acct, weekly.ID, "user:lifter", later)
	if err != nil {
		t.Fatal(err)
	}
	if lifted.LiftedAt == nil || lifted.LiftedBy != "user:lifter" {
		t.Fatalf("lifted = %+v", lifted)
	}
	if _, err := store.LiftReleaseFreeze(fx.Ctx, acct, weekly.ID, "user:lifter", later); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second lift err = %v, want ErrNotFound", err)
	}
	if _, err := store.LiftReleaseFreeze(fx.Ctx, uuid.NewString(), policy.ID, "user:other", later); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lift from another account err = %v, want ErrNotFound", err)
	}
	listed, err = store.ListReleaseFreezes(fx.Ctx, acct, later)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range listed {
		if f.ID == weekly.ID {
			t.Fatalf("lifted freeze still listed: %+v", listed)
		}
	}
}
