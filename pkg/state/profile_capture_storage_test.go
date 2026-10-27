// adr: 1155
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

// TestProfileCaptureStorageLimit pins the account's capture storage limit: a
// capture is queued only while the unexpired stored profiles, plus the largest
// capture size for every capture still queued or running and for the new one,
// fit. It applies across the account's apps and to automatic captures, and
// expired captures stop counting before the sweep deletes them.
func TestProfileCaptureStorageLimit(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	const largest = api.ProfileCaptureMaxProfileBytes
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
			acct, err := store.CreateAccount(ctx, "profile-capture-storage@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			var apps []string
			for _, slug := range []string{"storage-one", "storage-two"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				apps = append(apps, app.ID)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			newCapture := func(at time.Time) api.ProfileCapture {
				return api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu"}, DurationSeconds: 5, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
			}
			finish := func(id string, at time.Time, blobs []state.ProfileCaptureBlob) {
				t.Helper()
				if _, err := store.ClaimProfileCapture(ctx, at); err != nil {
					t.Fatal(err)
				}
				result := api.ProfileCapture{Status: api.ProfileCaptureFailed, Reason: "no collector answered"}
				if len(blobs) > 0 {
					result = api.ProfileCapture{Status: api.ProfileCaptureReady, Processes: 1,
						Profiles: []api.ProfileCaptureProfile{{Kind: "cpu", ProcessID: "7", Bytes: len(blobs[0].Profile)}}}
				}
				if err := store.FinishProfileCapture(ctx, id, result, blobs, at); err != nil {
					t.Fatal(err)
				}
			}
			const limit = 2*largest + 1000
			first := newCapture(now)
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], first, limit); err != nil {
				t.Fatal(err)
			}
			finish(first.ID, now.Add(time.Second), []state.ProfileCaptureBlob{{Kind: "cpu", ProcessID: "7", Profile: make([]byte, 1500)}})

			// 1500 stored bytes leave room for one more capture of the largest size.
			running := newCapture(now.Add(2 * time.Second))
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], running, limit); err != nil {
				t.Fatalf("capture within the limit refused: %v", err)
			}
			// The running capture reserves the largest size, so another app's
			// capture no longer fits, and neither does an automatic one.
			err = store.CreateProfileCapture(ctx, acct.ID, apps[1], newCapture(now.Add(3*time.Second)), limit)
			var full *state.ProfileCaptureStorageError
			if !errors.Is(err, state.ErrProfileCaptureStorage) || !errors.As(err, &full) || full.Limit != limit || full.Used != 1500+largest {
				t.Fatalf("capture past the limit = %v (%+v), want storage full at %d of %d bytes", err, full, 1500+largest, int64(limit))
			}
			automatic := newCapture(now.Add(3 * time.Second))
			automatic.Trigger = api.ProfileCaptureTriggerMemoryPressure
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, apps[1], automatic, time.Hour, limit); !errors.Is(err, state.ErrProfileCaptureStorage) {
				t.Fatalf("automatic capture past the limit = %v, want storage full", err)
			}

			// A finished capture without profiles frees its reservation.
			finish(running.ID, now.Add(4*time.Second), nil)
			second := newCapture(now.Add(5 * time.Second))
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[1], second, limit); err != nil {
				t.Fatalf("capture after the reservation was released refused: %v", err)
			}
			finish(second.ID, now.Add(6*time.Second), nil)

			// The stored bytes count until the capture expires, not after.
			const tight = largest + 1000
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], newCapture(now.Add(7*time.Second)), tight); !errors.Is(err, state.ErrProfileCaptureStorage) {
				t.Fatalf("stored bytes not counted: %v", err)
			}
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], newCapture(first.ExpiresAt.Add(time.Second)), tight); err != nil {
				t.Fatalf("expired capture still counted: %v", err)
			}
		})
	}
}
