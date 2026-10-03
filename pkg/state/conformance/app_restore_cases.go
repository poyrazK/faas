package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testAppRestoreHonoursQuota pins that a restored app counts against the
// deployed-app quota like a new one. RestoreApp flipped the tombstone back
// to active without a count, so delete → create → restore gave any plan an
// unlimited number of live apps.
func testAppRestoreHonoursQuota(t *testing.T, fx *Fixture) {
	oneApp := api.Limits{DeployedApps: 1, DeveloperApps: 1}
	if _, err := fx.Store.ScheduleAppDeletion(fx.Ctx, fx.App.ID, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}
	if _, err := fx.Store.CreateAppIfUnderQuota(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "restore-quota-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1,
	}, oneApp); err != nil {
		t.Fatalf("CreateAppIfUnderQuota into the freed slot: %v", err)
	}
	_, err := fx.Store.RestoreApp(fx.Ctx, fx.App.ID, oneApp)
	var qe *state.QuotaError
	if !errors.As(err, &qe) || qe.Limit != 1 || qe.Observed != 1 {
		t.Fatalf("RestoreApp at the quota: err = %v, want *QuotaError{Limit:1, Observed:1}", err)
	}
	if got, err := fx.Store.AppBySlugIncludingDeleted(fx.Ctx, fx.App.Slug); err != nil || got.Status != state.AppDeleted {
		t.Fatalf("refused restore changed the tombstone: %+v, %v", got, err)
	}
	restored, err := fx.Store.RestoreApp(fx.Ctx, fx.App.ID, api.Limits{DeployedApps: 2, DeveloperApps: 2})
	if err != nil || restored.Status != state.AppActive {
		t.Fatalf("RestoreApp under the quota: %+v, %v", restored, err)
	}
}

// testAppRestoreKeepsCrons pins that deleting an app suspends its crons
// rather than deleting them, that a deploy going live does not re-arm
// them, and that restoring the app brings them back. Deletion used to
// delete the crons outright, so restoring an app inside its grace window
// silently lost every schedule.
func testAppRestoreKeepsCrons(t *testing.T, fx *Fixture) {
	cron, err := fx.Store.CreateCron(fx.Ctx, fx.App.ID, "*/5 * * * *", "/tick", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	if _, err := fx.Store.ScheduleAppDeletion(fx.Ctx, fx.App.ID, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}
	if _, err := fx.Store.CronByID(fx.Ctx, cron.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("CronByID of a deleted app's cron: err = %v, want ErrNotFound", err)
	}
	enabled, err := fx.Store.ListEnabledCrons(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range enabled {
		if c.ID == cron.ID {
			t.Fatal("a deleted app's cron is still listed for dispatch")
		}
	}
	suspension, ok := fx.Store.(state.CronSuspensionStore)
	if !ok {
		t.Fatal("store does not implement CronSuspensionStore")
	}
	if _, err := suspension.ReactivateCronsForApp(fx.Ctx, fx.App.ID); err != nil {
		t.Fatalf("ReactivateCronsForApp: %v", err)
	}
	if _, err := fx.Store.CronByID(fx.Ctx, cron.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("reactivation (a deploy going live) re-armed a deleted app's cron: err = %v", err)
	}
	if _, err := fx.Store.RestoreApp(fx.Ctx, fx.App.ID, api.Limits{DeployedApps: 100, DeveloperApps: 100}); err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	back, err := fx.Store.CronByID(fx.Ctx, cron.ID)
	if err != nil || back.SuspendedReason != "" || !back.Enabled || back.Schedule != "*/5 * * * *" {
		t.Fatalf("cron after restore = %+v, %v; want it back, enabled and unsuspended", back, err)
	}
}
