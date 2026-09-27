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
