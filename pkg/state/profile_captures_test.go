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

type profileCaptureBackend interface {
	state.Store
	state.ProfileCaptureStore
	state.ProfileCaptureQueue
}

func TestProfileCaptureLifecycle(t *testing.T) {
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
			acct, err := store.CreateAccount(ctx, "profile-capture@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "profile-capture", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			other, err := store.CreateAccount(ctx, "profile-capture-other@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			newCapture := func(at time.Time) api.ProfileCapture {
				return api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu", "heap"}, DurationSeconds: 5, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
			}
			c := newCapture(now)
			if err := store.CreateProfileCapture(ctx, other.ID, app.ID, c); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign account queued a capture: %v", err)
			}
			if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, c); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, newCapture(now)); !errors.Is(err, state.ErrProfileCaptureActive) {
				t.Fatalf("second active capture admitted: %v", err)
			}
			got, err := store.GetProfileCapture(ctx, acct.ID, app.ID, c.ID)
			if err != nil || got.Status != api.ProfileCaptureQueued || len(got.Kinds) != 2 || got.DurationSeconds != 5 || got.AppID != app.ID {
				t.Fatalf("queued capture: %+v %v", got, err)
			}
			if _, err := store.GetProfileCapture(ctx, other.ID, app.ID, c.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign account read a capture: %v", err)
			}

			claimed, err := store.ClaimProfileCapture(ctx, now)
			if err != nil || claimed.ID != c.ID || claimed.AppID != app.ID || claimed.AccountID != acct.ID || claimed.Capture.DurationSeconds != 5 {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			if _, err := store.ClaimProfileCapture(ctx, now); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("capture claimed twice: %v", err)
			}
			blobs := []state.ProfileCaptureBlob{{Kind: "cpu", ProcessID: "7", Profile: []byte("cpu-pprof")}, {Kind: "heap", ProcessID: "7", Profile: []byte("heap-pprof")}}
			result := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: uuid.NewString(), Processes: 1,
				Profiles: []api.ProfileCaptureProfile{{Kind: "cpu", ProcessID: "7", Bytes: 9}, {Kind: "heap", ProcessID: "7", Bytes: 10}}}
			if err := store.FinishProfileCapture(ctx, c.ID, result, blobs, now.Add(7*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := store.FinishProfileCapture(ctx, c.ID, result, nil, now); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("finished twice: %v", err)
			}
			got, err = store.GetProfileCapture(ctx, acct.ID, app.ID, c.ID)
			if err != nil || got.Status != api.ProfileCaptureReady || got.InstanceID != result.InstanceID || got.Processes != 1 || len(got.Profiles) != 2 || got.CompletedAt == nil {
				t.Fatalf("ready capture: %+v %v", got, err)
			}
			stored, err := store.ProfileCaptureBlobs(ctx, acct.ID, app.ID, c.ID)
			if err != nil || len(stored) != 2 || string(stored[1].Profile) != "heap-pprof" || stored[0].ProcessID != "7" {
				t.Fatalf("blobs: %+v %v", stored, err)
			}

			stale := newCapture(now.Add(time.Second))
			if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, stale); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ClaimProfileCapture(ctx, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ExpireProfileCaptures(ctx, now.Add(time.Second+state.ProfileCaptureInterruptedAfter+time.Second)); err != nil {
				t.Fatal(err)
			}
			if got, _ := store.GetProfileCapture(ctx, acct.ID, app.ID, stale.ID); got.Status != api.ProfileCaptureFailed || got.Reason == "" {
				t.Fatalf("interrupted capture not failed: %+v", got)
			}
			list, err := store.ListProfileCaptures(ctx, acct.ID, app.ID)
			if err != nil || len(list) != 2 || list[0].ID != stale.ID {
				t.Fatalf("list newest first: %+v %v", list, err)
			}
			if _, err := store.ExpireProfileCaptures(ctx, now.Add(api.ProfileCaptureRetention+time.Hour)); err != nil {
				t.Fatal(err)
			}
			if list, _ := store.ListProfileCaptures(ctx, acct.ID, app.ID); len(list) != 0 {
				t.Fatalf("expired captures retained: %d", len(list))
			}
		})
	}
}
