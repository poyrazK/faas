// adr: 1155 — automatic captures count against their own hourly quota, use at
// most their share of the account's capture storage (rotating out their
// oldest, never an OOM-linked one), are claimed after manual captures, and a
// customer can delete any capture.
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

func profileCaptureTestStore(t *testing.T, backend string) profileCaptureBackend {
	t.Helper()
	if backend == "mem" {
		return state.NewMemStore()
	}
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool)
}

func TestProfileCaptureAutomaticLimits(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	const largest = api.ProfileCaptureMaxProfileBytes
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := profileCaptureTestStore(t, backend)
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "profile-capture-automatic@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			var apps []string
			for _, slug := range []string{"auto-one", "auto-two", "auto-three"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				apps = append(apps, app.ID)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			capture := func(at time.Time, trigger string) api.ProfileCapture {
				return api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{api.ProfileKindHeap}, DurationSeconds: 5, Trigger: trigger,
					CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
			}
			// run claims the next capture, records its instance and finishes it
			// with one profile of size bytes (none: failed).
			run := func(want string, at time.Time, instance string, size int) {
				t.Helper()
				claimed, err := store.ClaimProfileCapture(ctx, at)
				if err != nil || claimed.ID != want {
					t.Fatalf("claim = %s, %v; want %s", claimed.ID, err, want)
				}
				if err := store.StartProfileCapture(ctx, want, instance, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
				result := api.ProfileCapture{Status: api.ProfileCaptureFailed, InstanceID: instance, Reason: "no collector answered"}
				var blobs []state.ProfileCaptureBlob
				if size > 0 {
					blobs = []state.ProfileCaptureBlob{{Kind: api.ProfileKindHeap, ProcessID: "7", Profile: make([]byte, size)}}
					result = api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: instance, Processes: 1, Profiles: []api.ProfileCaptureProfile{{Kind: api.ProfileKindHeap, ProcessID: "7", Bytes: size}}}
				}
				if err := store.FinishProfileCapture(ctx, want, result, blobs, at.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			queueAutomatic := func(app string, c api.ProfileCapture, limit int64) error {
				return store.QueueAutomaticProfileCapture(ctx, acct.ID, app, c, time.Nanosecond, limit)
			}

			// Automatic captures may use half the limit: room for one reservation
			// plus 2000 stored bytes.
			const limit = 2*largest + 4000
			oomLinked := capture(now, api.ProfileCaptureTriggerMemoryPressure)
			if err := queueAutomatic(apps[0], oomLinked, limit); err != nil {
				t.Fatal(err)
			}
			run(oomLinked.ID, now.Add(time.Second), "instance-a", 900)
			if _, err := store.StampProfileCaptureOOM(ctx, apps[0], "instance-a", state.ProfileCaptureOOM{KilledAt: now.Add(3 * time.Second), PeakMB: 500, PlanMB: 512}, now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			older := capture(now.Add(4*time.Second), api.ProfileCaptureTriggerCPUSaturation)
			if err := queueAutomatic(apps[1], older, limit); err != nil {
				t.Fatal(err)
			}
			run(older.ID, now.Add(5*time.Second), "instance-b", 900)
			newer := capture(now.Add(6*time.Second), api.ProfileCaptureTriggerSlowRequest)
			if err := queueAutomatic(apps[2], newer, limit); err != nil {
				t.Fatal(err)
			}
			run(newer.ID, now.Add(7*time.Second), "instance-c", 900)

			// 2700 automatic bytes exceed the share: the oldest capture without
			// an OOM link is rotated out to make room; the OOM-linked one stays.
			rotating := capture(now.Add(8*time.Second), api.ProfileCaptureTriggerCPUSaturation)
			if err := queueAutomatic(apps[1], rotating, limit); err != nil {
				t.Fatalf("automatic capture with rotatable room refused: %v", err)
			}
			if _, err := store.GetProfileCapture(ctx, acct.ID, apps[1], older.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("oldest automatic capture not rotated out: %v", err)
			}
			for app, id := range map[string]string{apps[0]: oomLinked.ID, apps[2]: newer.ID} {
				if _, err := store.GetProfileCapture(ctx, acct.ID, app, id); err != nil {
					t.Fatalf("capture %s rotated out: %v", id, err)
				}
			}

			// With the share held by a reservation and the OOM-linked capture,
			// another automatic capture is refused for the share, not the limit,
			// and nothing is rotated out for it.
			refused := capture(now.Add(9*time.Second), api.ProfileCaptureTriggerSlowRequest)
			err = queueAutomatic(apps[2], refused, limit)
			var full *state.ProfileCaptureStorageError
			if !errors.As(err, &full) || !full.Automatic || full.Limit != limit/2 {
				t.Fatalf("automatic capture past its share = %v (%+v)", err, full)
			}
			if _, err := store.GetProfileCapture(ctx, acct.ID, apps[2], newer.ID); err != nil {
				t.Fatalf("a refused capture rotated out %s: %v", newer.ID, err)
			}

			// A manual capture is claimed before the older queued automatic one
			// and fits the half automatic captures cannot use.
			manual := capture(now.Add(10*time.Second), "")
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0], manual, limit); err != nil {
				t.Fatalf("manual capture refused beside automatic ones: %v", err)
			}
			run(manual.ID, now.Add(11*time.Second), "instance-a", 0)
			run(rotating.ID, now.Add(12*time.Second), "instance-b", 0)

			// A customer deletes a capture and its profiles, once.
			if err := store.DeleteProfileCapture(ctx, uuid.NewString(), apps[2], newer.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("another account deleted a capture: %v", err)
			}
			if err := store.DeleteProfileCapture(ctx, acct.ID, apps[2], newer.ID); err != nil {
				t.Fatal(err)
			}
			if blobs, err := store.ProfileCaptureBlobs(ctx, acct.ID, apps[2], newer.ID); err != nil || len(blobs) != 0 {
				t.Fatalf("deleted capture's profiles = %d, %v", len(blobs), err)
			}
			if err := store.DeleteProfileCapture(ctx, acct.ID, apps[2], newer.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second delete = %v", err)
			}
			// Deleting a queued capture cancels it.
			queued := capture(now.Add(13*time.Second), "")
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[2], queued, limit); err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteProfileCapture(ctx, acct.ID, apps[2], queued.ID); err != nil {
				t.Fatal(err)
			}
			if claimed, err := store.ClaimProfileCapture(ctx, now.Add(14*time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted queued capture claimed: %s, %v", claimed.ID, err)
			}
		})
	}
}

// TestProfileCaptureQuotasAreSeparate pins that automatic captures use their
// own hourly quota, so they never use up the captures a person can start.
func TestProfileCaptureQuotasAreSeparate(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := profileCaptureTestStore(t, backend)
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "profile-capture-quotas@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "quotas", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			capture := func(i int, trigger string) api.ProfileCapture {
				at := now.Add(time.Duration(i) * time.Second)
				return api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{api.ProfileKindCPU}, DurationSeconds: 5, Trigger: trigger,
					CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
			}
			done := func(id string, i int) {
				t.Helper()
				at := now.Add(time.Duration(i) * time.Second)
				if _, err := store.ClaimProfileCapture(ctx, at); err != nil {
					t.Fatal(err)
				}
				if err := store.FinishProfileCapture(ctx, id, api.ProfileCapture{Status: api.ProfileCaptureFailed, Reason: "r"}, nil, at); err != nil {
					t.Fatal(err)
				}
			}
			for i := range api.ProfileCaptureAutomaticMaxPerAccountHour {
				c := capture(i, api.ProfileCaptureTriggerCPUSaturation)
				if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, c, time.Nanosecond, profileCaptureStorage); err != nil {
					t.Fatalf("automatic capture %d: %v", i, err)
				}
				done(c.ID, i)
			}
			next := api.ProfileCaptureAutomaticMaxPerAccountHour
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, capture(next, api.ProfileCaptureTriggerSlowRequest), time.Nanosecond, profileCaptureStorage); !errors.Is(err, state.ErrProfileCaptureQuota) {
				t.Fatalf("automatic capture past its hourly quota = %v", err)
			}
			if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, capture(next, ""), profileCaptureStorage); err != nil {
				t.Fatalf("automatic captures used up the manual quota: %v", err)
			}
		})
	}
}
