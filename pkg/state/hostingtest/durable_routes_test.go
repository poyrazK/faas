package hostingtest

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedDurable(t *testing.T, store hostingTestStore) (state.Account, api.HostingSite) {
	t.Helper()
	account, base := seed(t, store)
	base.ID = uuid.NewString()
	base.Hostname = base.ID + ".hosting.example"
	base.Name = "durable"
	base.DurableRouting = true
	site, err := store.CreateHostingSite(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	return account, site
}

func durableLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610151515 — intent and outbox generations commit together.
	account, site := seedDurable(t, store)
	snapshot, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || snapshot.Generation != 1 || snapshot.PublishedGeneration != 0 {
		t.Fatalf("initial outbox: %+v %v", snapshot, err)
	}
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	site, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, release.ID, site.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || snapshot.Generation != 3 || len(snapshot.Releases) != 1 || snapshot.Site.ActiveReleaseID != release.ID {
		t.Fatalf("ready/promotion snapshot: %+v %v", snapshot, err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.RetryHostingRoute(t.Context(), site.ID, 2, time.Now().Add(24*time.Hour), api.HostingRouteErrorProvider); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueHostingRoutePublications(t.Context(), time.Now().Add(time.Second), api.HostingRoutePageSize)
	if err != nil || !slices.Contains(due, site.ID) {
		t.Fatalf("old retry deferred newer intent: %v %v", due, err)
	}
	site, err = store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, site.Revision, true)
	if err != nil || !site.Suspended {
		t.Fatalf("suspend: %+v %v", site, err)
	}
	if _, err = store.SetHostingSiteSuspended(t.Context(), uuid.NewString(), site.ID, site.Revision, false); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account suspension: %v", err)
	}
	if _, err = store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, site.Revision-1, false); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale suspension: %v", err)
	}
	snapshot, err = store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || snapshot.Generation != 4 {
		t.Fatalf("suspension generation: %+v %v", snapshot, err)
	}
	at := time.Now()
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, 4, at); err != nil {
		t.Fatal(err)
	}
	due, err = store.ListDueHostingRoutePublications(t.Context(), at.Add(time.Minute), api.HostingRoutePageSize)
	if err != nil || slices.Contains(due, site.ID) {
		t.Fatalf("unchanged route remained due: %v %v", due, err)
	}
	due, err = store.ListDueHostingRoutePublications(t.Context(), at.Add(api.HostingRouteRepairInterval+time.Second), api.HostingRoutePageSize)
	if err != nil || !slices.Contains(due, site.ID) {
		t.Fatalf("periodic repair missing: %v %v", due, err)
	}
	site, err = store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, site.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := store.HostingRouteSnapshot(t.Context(), site.ID)
	if _, err = store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, site.Revision, false); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("resumed deleting site: %v", err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, previous.Generation, time.Now().Add(-2*api.HostingRetirementGrace)); err != nil {
		t.Fatal(err)
	}
	retired, err := store.GetHostingRelease(t.Context(), account.ID, site.ID, release.ID)
	if err != nil || retired.RetirementPublishedAt != nil {
		t.Fatalf("old acknowledgement qualified new retirement: %+v %v", retired, err)
	}
	if err = store.DeleteHostingReleaseRecord(t.Context(), account.ID, site.ID, release.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("removed artifacts without revocation receipt: %v", err)
	}
	latest, _ := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, latest.Generation, time.Now()); err != nil {
		t.Fatal(err)
	}
	retired, err = store.GetHostingRelease(t.Context(), account.ID, site.ID, release.ID)
	if err != nil || retired.RetirementPublishedAt == nil {
		t.Fatalf("retirement not acknowledged: %+v %v", retired, err)
	}
	if err = store.DeleteHostingReleaseRecord(t.Context(), account.ID, site.ID, release.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("removed artifacts before receipt grace: %v", err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, latest.Generation+1, time.Now()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("acknowledged future generation: %v", err)
	}
}

func durableEmptySiteCleanup(t *testing.T, store hostingTestStore) {
	account, site := seedDurable(t, store)
	if _, err := store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.PurgeHostingSite(t.Context(), account.ID, site.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("purged missing tombstone acknowledgement: %v", err)
	}
	snapshot, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Model an already-aged provider acknowledgement without a wall-clock sleep.
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, snapshot.Generation, time.Now().Add(-api.HostingRetirementGrace-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.HostingRouteSnapshot(t.Context(), site.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("outbox survived catalog cleanup: %v", err)
	}
}

func durableReleaseCleanup(t *testing.T, store hostingTestStore) {
	account, site := seedDurable(t, store)
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, snapshot.Generation, time.Now().Add(-api.HostingRetirementGrace-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingSite(t.Context(), account.ID, site.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("purged a site with retained release metadata: %v", err)
	}
	if err = store.DeleteHostingReleaseRecord(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatalf("aged revocation did not allow release cleanup: %v", err)
	}
	if err = store.PurgeHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatalf("completed release cleanup did not allow site removal: %v", err)
	}
}

func durableSnapshotConcurrency(t *testing.T, store hostingTestStore) {
	account, site := seedDurable(t, store)
	var wg sync.WaitGroup
	failures := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		current := site
		for i := 0; i < 30; i++ {
			next, err := store.SetHostingSiteSuspended(t.Context(), account.ID, site.ID, current.Revision, i%2 == 0)
			if err != nil {
				failures <- err
				return
			}
			current = next
		}
	}()
	for range 30 {
		snapshot, err := store.HostingRouteSnapshot(t.Context(), site.ID)
		if err != nil {
			t.Error(err)
			break
		}
		if snapshot.Generation != snapshot.Site.Revision {
			t.Errorf("mixed transaction generations: route=%d site=%d", snapshot.Generation, snapshot.Site.Revision)
			break
		}
	}
	wg.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}
