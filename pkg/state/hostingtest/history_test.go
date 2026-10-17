package hostingtest

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingHistoryLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610170902 — metadata is immutable and history commits with promotion CAS.
	account, site := seed(t, store)
	ctx := t.Context()
	source := &api.HostingReleaseProvenance{Repository: "org/site", CommitSHA: strings.Repeat("a", 40), Branch: "main", Description: "Initial release"}
	first, err := store.CreateHostingReleaseWithProvenance(ctx, account.ID, site.ID, manifest(), source)
	if err != nil {
		t.Fatal(err)
	}
	source.Description = "caller mutation"
	if first.Provenance.Description != "Initial release" {
		t.Fatal("input alias")
	}
	first.Provenance.Description = "return mutation"
	first, err = store.GetHostingRelease(ctx, account.ID, site.ID, first.ID)
	if err != nil || first.Provenance.Description != "Initial release" {
		t.Fatal("return alias", err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	actor := api.HostingPromotionActor{AccountID: account.ID, KeyID: uuid.NewString()}
	var wg sync.WaitGroup
	var wins atomic.Int64
	for range 8 {
		wg.Go(func() {
			_, err := store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, first.ID, site.Revision, actor)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("CAS wins", wins.Load())
	}
	page, err := store.ListHostingHistory(ctx, account.ID, site.ID, 0)
	if err != nil || len(page.Events) != 1 {
		t.Fatal(page, err)
	}
	event := page.Events[0]
	if event.Revision != 2 || event.PreviousRevision != 1 || event.PreviousReleaseID != "" || event.ReleaseID != first.ID || event.Actor != actor || event.AcceptedAt.IsZero() || event.Provenance.Description != "Initial release" {
		t.Fatal(event)
	}
	page.Events[0].Provenance.Description = "history return mutation"
	page, _ = store.ListHostingHistory(ctx, account.ID, site.ID, 0)
	if page.Events[0].Provenance.Description != "Initial release" {
		t.Fatal("history alias")
	}
	if _, err = store.ListHostingHistory(ctx, uuid.NewString(), site.ID, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign history", err)
	}
	second, err := store.CreateHostingRelease(ctx, account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, second.ID, 2, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, first.ID, 3, actor); err != nil {
		t.Fatal(err)
	}
	page, _ = store.ListHostingHistory(ctx, account.ID, site.ID, 0)
	if len(page.Events) != 3 || page.Events[0].PreviousReleaseID != second.ID || page.Events[0].ReleaseID != first.ID {
		t.Fatal("rollback missing", page)
	}
	// Cleanup must not delete history snapshots or block retirement.
	if _, err = store.RetireHostingRelease(ctx, account.ID, site.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	page, _ = store.ListHostingHistory(ctx, account.ID, site.ID, 0)
	if page.Events[1].ReleaseID != second.ID {
		t.Fatal("history lost after release GC")
	}
	// Legacy/off path remains usable and does not fabricate entries.
	if _, err = store.PromoteHostingRelease(ctx, account.ID, site.ID, first.ID, 4); err != nil {
		t.Fatal(err)
	}
	page, _ = store.ListHostingHistory(ctx, account.ID, site.ID, 0)
	if len(page.Events) != 3 {
		t.Fatal("legacy capture unexpectedly enabled")
	}
	_, err = store.DeleteHostingSite(ctx, account.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingSite(ctx, account.ID, site.ID); err != nil {
		t.Fatal("history blocked site purge", err)
	}
	if _, err = store.ListHostingHistory(ctx, account.ID, site.ID, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
}
func hostingHistoryRetention(t *testing.T, store hostingTestStore) {
	// adr: 202610170902 — pagination/retention is bounded and never changes accepted intent.
	account, site := seed(t, store)
	ctx := t.Context()
	r, err := store.CreateHostingRelease(ctx, account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor := api.HostingPromotionActor{AccountID: account.ID}
	for range api.HostingHistoryPerSite + 2 {
		site, err = store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, r.ID, site.Revision, actor)
		if err != nil {
			t.Fatal(err)
		}
	}
	before := int64(0)
	seen := map[int64]bool{}
	last := site.Revision + 1
	for {
		page, err := store.ListHostingHistory(ctx, account.ID, site.ID, before)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) > api.HostingHistoryPageSize || page.RetentionLimit != api.HostingHistoryPerSite {
			t.Fatal(page)
		}
		for _, e := range page.Events {
			if e.Revision >= last || seen[e.Revision] || e.AcceptedAt.IsZero() {
				t.Fatal("unstable history", e)
			}
			last = e.Revision
			seen[e.Revision] = true
		}
		if page.NextBeforeRevision == 0 {
			break
		}
		if page.NextBeforeRevision != last {
			t.Fatal("bad cursor")
		}
		before = page.NextBeforeRevision
	}
	if len(seen) != api.HostingHistoryPerSite || seen[2] || seen[3] {
		t.Fatal("bad retention", len(seen))
	}
}
