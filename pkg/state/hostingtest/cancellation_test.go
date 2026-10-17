package hostingtest

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cancelIntent(t *testing.T, store hostingTestStore, account string, site api.HostingSite, ready bool) (api.HostingCIRun, api.HostingCIRunRequest) {
	t.Helper()
	req := api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600, ReservePreview: true}
	run, err := store.CreateHostingCIRun(t.Context(), account, site.ID, uuid.NewString(), req)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		if _, err = store.ReadyHostingRelease(t.Context(), account, site.ID, run.ReleaseID); err != nil {
			t.Fatal(err)
		}
	}
	return run, req
}
func hostingCancellation(t *testing.T, store hostingTestStore) {
	// adr: 202610171242 — terminal cancellation frees unused holds, never storage ahead of safe GC.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	run, req := cancelIntent(t, store, account.ID, site, false)
	usage, err := store.GetHostingUsage(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CancelHostingCIRun(ctx, uuid.NewString(), site.ID, run.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign cancellation", err)
	}
	canceled, err := store.CancelHostingCIRun(ctx, account.ID, site.ID, run.ID)
	expected := run
	expected.Canceled = true
	if err != nil || canceled != expected {
		t.Fatal(canceled, err)
	}
	if c, e := store.GetHostingPreviewCapacity(ctx, account.ID, site.ID); e != nil || c.Reservations != 0 || c.Available != api.HostingPreviewsPerSite {
		t.Fatal(c, e)
	}
	if u, e := store.GetHostingUsage(ctx, account.ID); e != nil || u.ReservedBytes != usage.ReservedBytes {
		t.Fatal("storage freed before GC", u, e)
	}
	release, err := store.GetHostingRelease(ctx, account.ID, site.ID, run.ReleaseID)
	if err != nil || release.State != "retiring" {
		t.Fatal(release, err)
	}
	for range 3 {
		again, e := store.CancelHostingCIRun(ctx, account.ID, site.ID, run.ID)
		if e != nil || again != expected {
			t.Fatal("non-idempotent cancel", again, e)
		}
	}
	after, err := store.GetHostingRelease(ctx, account.ID, site.ID, run.ReleaseID)
	if err != nil || !after.RetiredAt.Equal(*release.RetiredAt) || after.RetirementGeneration != release.RetirementGeneration {
		t.Fatal("cleanup extended", after, err)
	}
	again, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, run.ID, req)
	if err != nil || again != expected {
		t.Fatal("late retry resurrected", again, err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("finalize resurrected", err)
	}
	if _, err = store.TouchHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("new uploads allowed", err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("preview resurrected", err)
	}
	if _, err = store.PromoteHostingRelease(ctx, account.ID, site.ID, run.ReleaseID, site.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatal("canceled promotion", err)
	}
	// Physical removal still requires the existing route acknowledgment and grace.
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, run.ReleaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("premature GC", err)
	}
	snap, err := store.HostingRouteSnapshot(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingRoute(ctx, site.ID, snap.Generation, time.Now().Add(-2*api.HostingRetirementGrace)); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if again, err = store.CancelHostingCIRun(ctx, account.ID, site.ID, run.ID); err != nil || again != expected {
		t.Fatal("GC lost terminal state", again, err)
	}
	if again, err = store.CreateHostingCIRun(ctx, account.ID, site.ID, run.ID, req); err != nil || again != expected {
		t.Fatal("GC retry resurrected", again, err)
	}
	// Original preview revocation is atomic with retirement, but rows continue holding capacity.
	previewRun, _ := cancelIntent(t, store, account.ID, site, true)
	preview, err := store.CreateHostingCIPreview(ctx, account.ID, site.ID, previewRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CancelHostingCIRun(ctx, account.ID, site.ID, previewRun.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListHostingPreviews(ctx, account.ID, site.ID)
	if err != nil || len(rows) != 1 || !rows[0].Revoked || rows[0].Generation != 2 {
		t.Fatal(rows, err)
	}
	if err = store.FinishHostingPreview(ctx, preview, time.Now(), ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale publication accepted", err)
	}
	if c, e := store.GetHostingPreviewCapacity(ctx, account.ID, site.ID); e != nil || c.Previews != 1 || c.Available != api.HostingPreviewsPerSite-1 {
		t.Fatal("preview freed before purge", c, e)
	}
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, previewRun.ReleaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("preview-pinned bytes removed", err)
	}
	if err = store.FinishHostingPreview(ctx, rows[0], time.Now().Add(-2*api.HostingPreviewRemovalGrace), ""); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingPreview(ctx, rows[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, err = store.HostingRouteSnapshot(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingRoute(ctx, site.ID, snap.Generation, time.Now().Add(-2*api.HostingRetirementGrace)); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, previewRun.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, previewRun.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("purged canceled preview recreated", err)
	}
	records, err := store.ListHostingActivityRecords(ctx, account.ID, site.ID, "")
	if err != nil || len(records.Records) != 2 || !records.Records[0].Run.Canceled {
		t.Fatal(records, err)
	}
}
func hostingCancellationProtection(t *testing.T, store hostingTestStore) {
	// adr: 202610171242 — production history and other preview references are protected atomically.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	run, _ := cancelIntent(t, store, account.ID, site, true)
	promoted, err := store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, run.ReleaseID, site.Revision, api.HostingPromotionActor{AccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := cancelIntent(t, store, account.ID, site, true)
	if _, err = store.PromoteHostingRelease(ctx, account.ID, site.ID, other.ReleaseID, promoted.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CancelHostingCIRun(ctx, account.ID, site.ID, run.ID); !errors.Is(err, state.ErrHostingCancellationProtected) {
		t.Fatal("rollback target canceled", err)
	}
	shared, _ := cancelIntent(t, store, account.ID, site, true)
	id := uuid.NewString()
	now := time.Now().UTC()
	p, err := store.CreateHostingPreview(ctx, api.HostingPreview{ID: id, AccountID: account.ID, SiteID: site.ID, ReleaseID: shared.ReleaseID, Hostname: "p-" + id + strings.TrimPrefix(site.Hostname, site.ID), OriginID: site.OriginID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CancelHostingCIRun(ctx, account.ID, site.ID, shared.ID); !errors.Is(err, state.ErrHostingCancellationProtected) {
		t.Fatal("other preview canceled", err)
	}
	kept, err := store.GetHostingCIRun(ctx, account.ID, site.ID, shared.ID)
	if err != nil || kept.Canceled {
		t.Fatal("partial cancellation", kept, err)
	}
	rows, err := store.ListHostingPreviews(ctx, account.ID, site.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != p.ID || rows[0].Revoked {
		t.Fatal("partial revocation", rows, err)
	}
}
func hostingCancellationRaces(t *testing.T, store hostingTestStore) {
	// adr: 202610171242 — either promotion wins and protects the release, or cancellation fences it.
	for _, mode := range []string{"promotion", "preview", "manual_preview", "finalize"} {
		t.Run(mode, func(t *testing.T) {
			account, site := seedDurable(t, store)
			ctx := t.Context()
			run, _ := cancelIntent(t, store, account.ID, site, mode != "finalize")
			var cancelErr, otherErr error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Go(func() { <-start; _, cancelErr = store.CancelHostingCIRun(ctx, account.ID, site.ID, run.ID) })
			wg.Go(func() {
				<-start
				switch mode {
				case "promotion":
					_, otherErr = store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, run.ReleaseID, site.Revision, api.HostingPromotionActor{AccountID: account.ID})
				case "preview":
					_, otherErr = store.CreateHostingCIPreview(ctx, account.ID, site.ID, run.ID)
				case "manual_preview":
					id := uuid.NewString()
					now := time.Now().UTC()
					_, otherErr = store.CreateHostingPreview(ctx, api.HostingPreview{ID: id, AccountID: account.ID, SiteID: site.ID, ReleaseID: run.ReleaseID, Hostname: "p-" + id + strings.TrimPrefix(site.Hostname, site.ID), OriginID: site.OriginID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
				case "finalize":
					_, otherErr = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID)
				}
			})
			close(start)
			wg.Wait()
			for _, err := range []error{cancelErr, otherErr} {
				if err != nil && !errors.Is(err, state.ErrConflict) {
					t.Fatal("unexpected race failure", err)
				}
			}
			current, err := store.GetHostingCIRun(ctx, account.ID, site.ID, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			release, err := store.GetHostingRelease(ctx, account.ID, site.ID, run.ReleaseID)
			if err != nil {
				t.Fatal(err)
			}
			if cancelErr == nil && (!current.Canceled || release.State != "retiring") {
				t.Fatal("cancellation lost", current, release)
			}
			if mode == "promotion" && otherErr == nil && (current.Canceled || release.FirstPromotedAt == nil) {
				t.Fatal("production canceled", current, release)
			}
			if mode != "promotion" && mode != "manual_preview" && cancelErr != nil {
				t.Fatal("cancellation should settle", cancelErr)
			}
			if mode == "manual_preview" && otherErr == nil && (current.Canceled || cancelErr == nil) {
				t.Fatal("manual preview not protected", current, cancelErr)
			}
			if current.Canceled {
				if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, run.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("late preview", err)
				}
			}
		})
	}
}
