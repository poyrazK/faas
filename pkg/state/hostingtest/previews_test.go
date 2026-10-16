package hostingtest

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

func previewLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610161954 — immutable preview references fence retirement and cleanup.
	account, site := seedDurable(t, store)
	r, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	input := func() api.HostingPreview {
		id := uuid.NewString()
		now := time.Now().UTC()
		return api.HostingPreview{ID: id, AccountID: account.ID, SiteID: site.ID, ReleaseID: r.ID, Hostname: "p-" + id + ".hosting.example", OriginID: site.OriginID, CreatedAt: now, ExpiresAt: now.Add(api.HostingPreviewTTLMin)}
	}
	if _, err = store.CreateHostingPreview(t.Context(), input()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("pending release preview", err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateHostingPreview(t.Context(), input())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ListHostingPreviews(t.Context(), uuid.NewString(), site.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign list", err)
	}
	if _, err = store.RevokeHostingPreview(t.Context(), uuid.NewString(), site.ID, p.ID, 1); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign revoke", err)
	}
	current, err := store.GetHostingSite(t.Context(), account.ID, site.ID)
	if err != nil || current.ActiveReleaseID != "" || current.Revision != site.Revision {
		t.Fatal("preview changed production intent", err)
	}
	head := hosting.NewDurableRoute(current, []api.HostingRelease{r}, 1)
	if head.Enabled || !head.PreviewAllowed || len(head.ReadyReleases) != 0 {
		t.Fatal("preview leaked staged release", head)
	}
	if _, err = store.RetireHostingRelease(t.Context(), account.ID, site.ID, r.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retired referenced release", err)
	}
	now := time.Now().UTC()
	if err = store.FinishHostingPreview(t.Context(), p, now, ""); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.RevokeHostingPreview(t.Context(), account.ID, site.ID, p.ID, 1)
	if err != nil || !revoked.Revoked || revoked.Generation != 2 {
		t.Fatal("revoke", revoked, err)
	}
	if err = store.FinishHostingPreview(t.Context(), p, now.Add(time.Hour), ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale completion postponed revoke", err)
	}
	if err = store.PurgeHostingPreview(t.Context(), revoked, now.Add(time.Hour)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("purge before receipt", err)
	}
	if err = store.FinishHostingPreview(t.Context(), revoked, now, ""); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingPreview(t.Context(), revoked, now); !errors.Is(err, state.ErrConflict) {
		t.Fatal("purge before grace", err)
	}
	// Repair must preserve the first receipt, or grace never completes.
	if err = store.FinishHostingPreview(t.Context(), revoked, now.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingPreview(t.Context(), revoked, now.Add(api.HostingPreviewRemovalGrace+time.Second)); err != nil {
		t.Fatal("safe purge", err)
	}
	if _, err = store.RetireHostingRelease(t.Context(), account.ID, site.ID, r.ID); err != nil {
		t.Fatal("retire after cleanup", err)
	}
}
func previewExpiryAndQuota(t *testing.T, store hostingTestStore) {
	// adr: 202610161954 — bounded references include pending revocations/expiry.
	account, site := seedDurable(t, store)
	r, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for range api.HostingPreviewsPerSite + 4 {
		wg.Go(func() {
			id := uuid.NewString()
			now := time.Now().UTC()
			_, err := store.CreateHostingPreview(t.Context(), api.HostingPreview{ID: id, AccountID: account.ID, SiteID: site.ID, ReleaseID: r.ID, Hostname: "p-" + id + strings.TrimPrefix(site.Hostname, site.ID), OriginID: site.OriginID, CreatedAt: now, ExpiresAt: now.Add(api.HostingPreviewTTLMin)})
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, state.ErrHostingQuota) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != api.HostingPreviewsPerSite {
		t.Fatal("racy quota", accepted.Load())
	}
	rows, err := store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if err != nil || len(rows) != api.HostingPreviewsPerSite {
		t.Fatal(err)
	}
	expired, err := store.PrepareHostingPreview(t.Context(), rows[0].ID, rows[0].ExpiresAt)
	if err != nil || !expired.Revoked || expired.Generation != 2 {
		t.Fatal("expiry", expired, err)
	}
	if _, err = store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rows {
		if !p.Revoked || p.Generation != 2 {
			t.Fatal("site deletion did not revoke atomically", p)
		}
	}
	if err = store.DeleteHostingReleaseRecord(t.Context(), account.ID, site.ID, r.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("catalog deletion with previews", err)
	}
	if err = store.PurgeHostingSite(t.Context(), account.ID, site.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("site purge with previews", err)
	}
}
