package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomaticProfileCaptureInterval(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store profileCaptureBackend = state.NewMemStore()
			if backend == "pg" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "auto-capture@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "auto-capture", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			auto := func(at time.Time) api.ProfileCapture {
				return api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"heap"}, DurationSeconds: 10, Trigger: api.ProfileCaptureTriggerMemoryPressure,
					TriggerDetail: "guest memory available fell to 30 MiB of 246 MiB", CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
			}
			finish := func() {
				t.Helper()
				claimed, err := store.ClaimProfileCapture(ctx, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if err := store.FinishProfileCapture(ctx, claimed.ID, api.ProfileCapture{Status: api.ProfileCaptureReady}, nil, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, api.ProfileCapture{ID: uuid.NewString(), CreatedAt: now}, time.Hour); err == nil {
				t.Fatal("automatic capture without a trigger accepted")
			}
			first := auto(now)
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, first, time.Hour); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetProfileCapture(ctx, acct.ID, app.ID, first.ID)
			if err != nil || got.Trigger != api.ProfileCaptureTriggerMemoryPressure || got.TriggerDetail == "" {
				t.Fatalf("automatic capture = %+v %v", got, err)
			}
			finish()
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, auto(now.Add(30*time.Minute)), time.Hour); !errors.Is(err, state.ErrProfileCaptureRecent) {
				t.Fatalf("second automatic capture inside the interval: %v", err)
			}
			// A manual capture is not limited by the automatic interval.
			manual := api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu"}, DurationSeconds: 5, CreatedAt: now.Add(31 * time.Minute), ExpiresAt: now.Add(api.ProfileCaptureRetention)}
			if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, manual); err != nil {
				t.Fatal(err)
			}
			if got, _ := store.GetProfileCapture(ctx, acct.ID, app.ID, manual.ID); got.Trigger != api.ProfileCaptureTriggerManual {
				t.Fatalf("manual capture trigger = %q", got.Trigger)
			}
			finish()
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, auto(now.Add(61*time.Minute)), time.Hour); err != nil {
				t.Fatalf("automatic capture after the interval: %v", err)
			}
		})
	}
}
