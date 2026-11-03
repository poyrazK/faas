// adr: 1155
package state_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProfileCaptureClaimCapsRunningPerPool pins that one pool runs at most
// ProfileCaptureMaxRunningPerPool captures at once, so another account's
// capture is claimed ahead of the busy pool's next one.
func TestProfileCaptureClaimCapsRunningPerPool(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := profileCaptureTestStore(t, backend)
			ctx := t.Context()
			busy, err := store.CreateAccount(ctx, "claim-busy@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			quiet, err := store.CreateAccount(ctx, "claim-quiet@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			queue := func(acct state.Account, slug string, at time.Time) string {
				t.Helper()
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				c := api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu"}, DurationSeconds: 5, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
				if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, c, profileCaptureStorage); err != nil {
					t.Fatal(err)
				}
				return c.ID
			}
			var busyIDs []string
			for i := range api.ProfileCaptureMaxRunningPerPool + 1 {
				busyIDs = append(busyIDs, queue(busy, "claim-busy-"+strconv.Itoa(i), now.Add(time.Duration(i)*time.Second)))
			}
			quietID := queue(quiet, "claim-quiet", now.Add(time.Minute))
			for i := range api.ProfileCaptureMaxRunningPerPool {
				claimed, err := store.ClaimProfileCapture(ctx, now.Add(2*time.Minute))
				if err != nil || claimed.ID != busyIDs[i] {
					t.Fatalf("claim %d = %s, %v; want the busy pool's %s", i, claimed.ID, err, busyIDs[i])
				}
			}
			claimed, err := store.ClaimProfileCapture(ctx, now.Add(2*time.Minute))
			if err != nil || claimed.ID != quietID {
				t.Fatalf("claim past the busy pool's cap = %s, %v; want the other account's %s", claimed.ID, err, quietID)
			}
			if claimed, err := store.ClaimProfileCapture(ctx, now.Add(2*time.Minute)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("the busy pool's next capture claimed while it runs its cap: %s, %v", claimed.ID, err)
			}
			if err := store.FinishProfileCapture(ctx, busyIDs[0], api.ProfileCapture{Status: api.ProfileCaptureFailed, Reason: "r"}, nil, now.Add(3*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if claimed, err := store.ClaimProfileCapture(ctx, now.Add(3*time.Minute)); err != nil || claimed.ID != busyIDs[len(busyIDs)-1] {
				t.Fatalf("claim after a busy capture finished = %s, %v", claimed.ID, err)
			}
		})
	}
}

// TestProfileCaptureSweepDeletesDeletedAppsCaptures pins that the expiry
// sweep removes a deleted app's captures: nobody can read or delete them, and
// they would hold their pool's storage until they expired.
func TestProfileCaptureSweepDeletesDeletedAppsCaptures(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			store := profileCaptureTestStore(t, backend)
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "sweep-deleted@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			var apps []state.App
			for _, slug := range []string{"sweep-gone", "sweep-kept"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				apps = append(apps, app)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			tight := state.ProfileCaptureLimits{StorageBytes: api.ProfileCaptureMaxProfileBytes + 1000}
			gone := api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu"}, DurationSeconds: 5, CreatedAt: now, ExpiresAt: now.Add(api.ProfileCaptureRetention)}
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[0].ID, gone, tight); err != nil {
				t.Fatal(err)
			}
			next := api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{"cpu"}, DurationSeconds: 5, CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(api.ProfileCaptureRetention)}
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[1].ID, next, tight); !errors.Is(err, state.ErrProfileCaptureStorage) {
				t.Fatalf("second capture in a full pool = %v", err)
			}
			if _, err := store.SoftDeleteAppCascade(ctx, apps[0].ID); err != nil {
				t.Fatal(err)
			}
			if n, err := store.ExpireProfileCaptures(ctx, now.Add(2*time.Second)); err != nil || n != 1 {
				t.Fatalf("sweep removed %d, %v; want the deleted app's capture", n, err)
			}
			if err := store.CreateProfileCapture(ctx, acct.ID, apps[1].ID, next, tight); err != nil {
				t.Fatalf("the deleted app's capture still holds the pool's storage: %v", err)
			}
		})
	}
}
