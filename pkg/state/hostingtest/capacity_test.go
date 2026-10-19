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
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingPreviewReservations(t *testing.T, store hostingTestStore) {
	// adr: 202610171216 — admission, manual writers and conversion share one atomic quota.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	req := api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600, ReservePreview: true}
	runs := []api.HostingCIRun{}
	for n := range api.HostingPreviewsPerSite - 2 {
		id := uuid.NewString()
		if n == 0 {
			id = uuid.Nil.String()
		}
		run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, id, req)
		if err != nil {
			t.Fatal(err)
		}
		if !run.PreviewReserved {
			t.Fatal("missing reservation")
		}
		if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	legacyReq := req
	legacyReq.ReservePreview = false
	legacy, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), legacyReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, legacy.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetHostingPreviewCapacity(ctx, uuid.NewString(), site.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign capacity", err)
	}
	var wg sync.WaitGroup
	var admitted atomic.Int64
	for i := range 9 {
		wg.Go(func() {
			var err error
			if i%3 == 0 {
				_, err = store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), req)
			} else {
				id := uuid.NewString()
				now := time.Now().UTC()
				_, err = store.CreateHostingPreview(ctx, api.HostingPreview{ID: id, AccountID: account.ID, SiteID: site.ID, ReleaseID: legacy.ReleaseID, Hostname: "p-" + id + strings.TrimPrefix(site.Hostname, site.ID), OriginID: site.OriginID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
			}
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, state.ErrHostingQuota) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 2 {
		t.Fatal("overbooked", admitted.Load())
	}
	capacity, err := store.GetHostingPreviewCapacity(ctx, account.ID, site.ID)
	if err != nil || capacity.Available != 0 || capacity.Previews+capacity.Reservations != api.HostingPreviewsPerSite {
		t.Fatal(capacity, err)
	}
	before, err := store.ListHostingReleases(ctx, account.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	rejected := uuid.NewString()
	if _, err = store.CreateHostingCIRun(ctx, account.ID, site.ID, rejected, req); !errors.Is(err, state.ErrHostingQuota) {
		t.Fatal("full admission", err)
	}
	after, err := store.ListHostingReleases(ctx, account.ID, site.ID)
	if err != nil || len(before) != len(after) {
		t.Fatal("partial allocation", len(before), len(after), err)
	}
	if _, err = store.GetHostingCIRun(ctx, account.ID, site.ID, rejected); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("rejected identity persisted", err)
	}
	original := runs[0]
	retried, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, original.ID, req)
	if err != nil || retried != original {
		t.Fatal("retry changed", retried, original, err)
	}
	if _, err = store.CreateHostingCIRun(ctx, account.ID, site.ID, original.ID, legacyReq); !errors.Is(err, state.ErrConflict) {
		t.Fatal("reservation mode changed", err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, legacy.ID); !errors.Is(err, state.ErrHostingQuota) {
		t.Fatal("legacy stole reservation", err)
	}
	preview, err := store.CreateHostingCIPreview(ctx, account.ID, site.ID, original.ID)
	if err != nil || preview.ID != original.PreviewID || !preview.ExpiresAt.Equal(original.PreviewExpiresAt) {
		t.Fatal("own reservation unusable", preview, err)
	}
	converted, err := store.GetHostingPreviewCapacity(ctx, account.ID, site.ID)
	if err != nil || converted.Available != 0 || converted.Previews != capacity.Previews+1 || converted.Reservations != capacity.Reservations-1 {
		t.Fatal("double counted conversion", converted, err)
	}
	// Revoked rows hold capacity until normal publication/grace/GC completes.
	if _, err = store.RevokeHostingPreview(ctx, account.ID, site.ID, preview.ID, preview.Generation); err != nil {
		t.Fatal(err)
	}
	if c, e := store.GetHostingPreviewCapacity(ctx, account.ID, site.ID); e != nil || c.Available != 0 {
		t.Fatal("unsafe revoked capacity", c, e)
	}
	if _, err = store.RetireHostingRelease(ctx, account.ID, site.ID, runs[1].ReleaseID); err != nil {
		t.Fatal(err)
	}
	freed, err := store.GetHostingPreviewCapacity(ctx, account.ID, site.ID)
	if err != nil || freed.Available != 1 || freed.Reservations != converted.Reservations-1 {
		t.Fatal("unused hold not released", freed, err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, runs[1].ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retired run reclaimed slot", err)
	}
}
