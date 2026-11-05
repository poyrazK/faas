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
			const limitBytes = 2*largest + 1000
			limit := state.ProfileCaptureLimits{StorageBytes: limitBytes}
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
			if !errors.Is(err, state.ErrProfileCaptureStorage) || !errors.As(err, &full) || full.Limit != limitBytes || full.Used != 1500+largest {
				t.Fatalf("capture past the limit = %v (%+v), want storage full at %d of %d bytes", err, full, 1500+largest, int64(limitBytes))
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
			tight := state.ProfileCaptureLimits{StorageBytes: largest + 1000}
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], newCapture(now.Add(7*time.Second)), tight); !errors.Is(err, state.ErrProfileCaptureStorage) {
				t.Fatalf("stored bytes not counted: %v", err)
			}
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], newCapture(first.ExpiresAt.Add(time.Second)), tight); err != nil {
				t.Fatalf("expired capture still counted: %v", err)
			}
		})
	}
}

// TestProfileCapturePool pins that captures count in their pool, not their
// creator's account: an organization's apps, created by different members,
// share one storage limit and hourly quota, and a member's personal apps stay
// outside it (ADR-1155).
func TestProfileCapturePool(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	const largest = api.ProfileCaptureMaxProfileBytes
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := profileCaptureTestStore(t, backend)
			ctx := t.Context()
			newApp := func(email, slug string) (string, string) {
				t.Helper()
				acct, err := store.CreateAccount(ctx, email, api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				return acct.ID, app.ID
			}
			alice, aliceApp := newApp("pool-alice@example.com", "pool-alice")
			bob, bobApp := newApp("pool-bob@example.com", "pool-bob")
			now := time.Now().UTC().Truncate(time.Millisecond)
			newCapture := func(i int) api.ProfileCapture {
				at := now.Add(time.Duration(i) * time.Second)
				return api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu"}, DurationSeconds: 5, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
			}
			org := state.ProfileCaptureLimits{StorageBytes: largest + 1000, Pool: uuid.NewString()}
			done := func(at int) {
				t.Helper()
				when := now.Add(time.Duration(at) * time.Second)
				claimed, err := store.ClaimProfileCapture(ctx, when)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.FinishProfileCapture(ctx, claimed.ID, api.ProfileCapture{Status: api.ProfileCaptureFailed, Reason: "no collector answered"}, nil, when); err != nil {
					t.Fatal(err)
				}
			}

			// Alice's queued capture reserves the org's room; Bob's capture of
			// another org app no longer fits, though Bob has stored nothing.
			if err := store.CreateProfileCapture(ctx, alice, aliceApp, newCapture(0), org); err != nil {
				t.Fatal(err)
			}
			err := store.CreateProfileCapture(ctx, bob, bobApp, newCapture(1), org)
			var full *state.ProfileCaptureStorageError
			if !errors.As(err, &full) || full.Used != largest {
				t.Fatalf("second member's capture in a full pool = %v (%+v), want storage full", err, full)
			}
			// Bob's own pool is untouched.
			personal := state.ProfileCaptureLimits{StorageBytes: largest + 1000}
			if err := store.CreateProfileCapture(ctx, bob, bobApp, newCapture(2), personal); err != nil {
				t.Fatalf("capture in the member's own pool refused: %v", err)
			}
			done(3)
			done(4)

			// The hourly quota is the pool's too: the members' automatic
			// captures use up one quota between them.
			roomy := state.ProfileCaptureLimits{StorageBytes: 1 << 40, Pool: org.Pool}
			automatic := func(i int) api.ProfileCapture {
				c := newCapture(i)
				c.Trigger = api.ProfileCaptureTriggerCPUSaturation
				return c
			}
			for i := range api.ProfileCaptureAutomaticMaxPerAccountHour {
				member, app := alice, aliceApp
				if i%2 == 1 {
					member, app = bob, bobApp
				}
				if err := store.QueueAutomaticProfileCapture(ctx, member, app, automatic(10+i), time.Nanosecond, roomy); err != nil {
					t.Fatalf("automatic capture %d: %v", i, err)
				}
				done(10 + i)
			}
			next := 10 + api.ProfileCaptureAutomaticMaxPerAccountHour
			if err := store.QueueAutomaticProfileCapture(ctx, bob, bobApp, automatic(next), time.Nanosecond, roomy); !errors.Is(err, state.ErrProfileCaptureQuota) {
				t.Fatalf("automatic capture past the pool's quota = %v", err)
			}
			roomyPersonal := state.ProfileCaptureLimits{StorageBytes: 1 << 40}
			if err := store.QueueAutomaticProfileCapture(ctx, bob, bobApp, automatic(next), time.Nanosecond, roomyPersonal); err != nil {
				t.Fatalf("the org's quota held back a personal capture: %v", err)
			}
		})
	}
}
