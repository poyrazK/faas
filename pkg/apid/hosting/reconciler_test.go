package hosting

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type routeSink struct {
	mu      sync.Mutex
	routes  []artifacts.Route
	failing map[string]bool
}

func (p *routeSink) Put(_ context.Context, route artifacts.Route) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failing[route.SiteID] {
		return errors.New("provider unavailable")
	}
	p.routes = append(p.routes, route)
	return nil
}

type routeArtifacts struct {
	removed       []string
	removeFailure bool
}

func (*routeArtifacts) ID() string { return strings.Repeat("a", 64) }
func (p *routeArtifacts) Remove(_ context.Context, _ string, release string, _ api.HostingManifest) error {
	if p.removeFailure {
		return errors.New("storage cleanup unavailable")
	}
	p.removed = append(p.removed, release)
	return nil
}

func routingSite(t *testing.T, store *state.MemStore, durable bool) (state.Account, api.HostingSite) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "web"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	site, err := store.CreateHostingSite(t.Context(), api.HostingSite{ID: id, AccountID: account.ID, ProjectID: project.ID, Environment: "production", Name: "web", Hostname: id + ".hosting.example", OriginID: strings.Repeat("a", 64), DurableRouting: durable})
	if err != nil {
		t.Fatal(err)
	}
	return account, site
}

func TestDurableReconcilerPersistsRetriesAndKeepsAdmissionSeparate(t *testing.T) {
	// adr: 202610151515 — admission state never becomes a durable serving heartbeat.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, release.ID, site.Revision); err != nil {
		t.Fatal(err)
	}
	sink := &routeSink{failing: map[string]bool{site.ID: true}}
	leased := &routeSink{}
	at := time.Now().Add(time.Second)
	r := Reconciler{Store: store, Artifacts: &routeArtifacts{}, LeasedRoutes: leased, DurableRoutes: sink, AdmissionEnabled: func() bool { return false }, Now: func() time.Time { return at }}
	if err = r.RunOnce(t.Context()); err == nil {
		t.Fatal("provider failure was lost")
	}
	snapshot, _ := store.HostingRouteSnapshot(t.Context(), site.ID)
	if snapshot.PublishedGeneration != 0 {
		t.Fatal("failed publication was acknowledged")
	}
	sink.failing[site.ID] = false
	at = at.Add(api.HostingRouteRetryInterval + time.Second)
	restarted := r
	if err = restarted.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sink.routes) != 1 || !sink.routes[0].Enabled || sink.routes[0].ActiveReleaseID != release.ID || len(leased.routes) != 0 {
		t.Fatalf("admission affected durable serving: %+v", sink.routes)
	}
	at = at.Add(10 * time.Minute)
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sink.routes) != 1 {
		t.Fatal("unchanged durable site emitted a heartbeat")
	}
	snapshot, _ = store.HostingRouteSnapshot(t.Context(), site.ID)
	if snapshot.PublishedGeneration != snapshot.Generation {
		t.Fatal("publication did not converge")
	}
}

func TestDurablePublicationFailureDoesNotBlockOtherSitesOrAllowCleanup(t *testing.T) {
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	_, other := routingSite(t, store, true)
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	provider := &routeSink{failing: map[string]bool{site.ID: true}}
	objects := &routeArtifacts{}
	at := time.Now().Add(2 * api.HostingRetirementGrace)
	r := Reconciler{Store: store, Artifacts: objects, LeasedRoutes: &routeSink{}, DurableRoutes: provider, AdmissionEnabled: func() bool { return false }, Now: func() time.Time { return at }}
	if err = r.RunOnce(t.Context()); err == nil {
		t.Fatal("expected publication failure")
	}
	successful, _ := store.HostingRouteSnapshot(t.Context(), other.ID)
	if successful.PublishedGeneration != successful.Generation {
		t.Fatal("failed site blocked another account")
	}
	if len(objects.removed) != 0 {
		t.Fatal("retirement age bypassed missing revocation acknowledgement")
	}
	loaded, _ := store.GetHostingRelease(t.Context(), account.ID, site.ID, release.ID)
	if loaded.RetirementPublishedAt != nil {
		t.Fatal("failed deletion received a cleanup receipt")
	}
	provider.failing[site.ID] = false
	at = at.Add(api.HostingRouteRetryInterval + time.Second)
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, _ = store.GetHostingRelease(t.Context(), account.ID, site.ID, release.ID)
	if loaded.RetirementPublishedAt == nil || len(objects.removed) != 0 {
		t.Fatal("cleanup did not wait for a full grace after acknowledgement")
	}
}

func TestLeasedReconcilerStillDisablesServingWithAdmission(t *testing.T) {
	store := state.NewMemStore()
	_, site := routingSite(t, store, false)
	leased := &routeSink{}
	durable := &routeSink{}
	r := Reconciler{Store: store, Artifacts: &routeArtifacts{}, LeasedRoutes: leased, DurableRoutes: durable, AdmissionEnabled: func() bool { return false }}
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(leased.routes) != 1 || leased.routes[0].Enabled || leased.routes[0].SiteID != site.ID || leased.routes[0].ExpiresAt == 0 || len(durable.routes) != 0 {
		t.Fatal("default leased behavior changed")
	}
}

type blockedRouteSink struct{ calls atomic.Int64 }

func (p *blockedRouteSink) Put(ctx context.Context, _ artifacts.Route) error {
	p.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func TestDurablePublicationDeadlinePreservesLeaseRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := state.NewMemStore()
		_, leasedSite := routingSite(t, store, false)
		for range api.HostingRoutePublishConcurrency + 1 {
			routingSite(t, store, true)
		}
		blocked, leased := &blockedRouteSink{}, &routeSink{}
		r := Reconciler{Store: store, Artifacts: &routeArtifacts{}, LeasedRoutes: leased, DurableRoutes: blocked, AdmissionEnabled: func() bool { return true }}
		start := time.Now()
		if err := r.RunOnce(t.Context()); err == nil {
			t.Fatal("publication timeout was lost")
		}
		if time.Since(start) != api.HostingRouteDispatchTimeout || blocked.calls.Load() != api.HostingRoutePublishConcurrency {
			t.Fatalf("unbounded dispatch: elapsed=%v calls=%d", time.Since(start), blocked.calls.Load())
		}
		if len(leased.routes) != 1 || leased.routes[0].SiteID != leasedSite.ID || !leased.routes[0].Enabled {
			t.Fatal("slow durable publications prevented lease renewal")
		}
		due, err := store.ListDueHostingRoutePublications(t.Context(), time.Now(), api.HostingRoutePageSize)
		if err != nil || len(due) != 1 {
			t.Fatalf("timed-out publications lost persistent retry scheduling: %v %v", due, err)
		}
	})
}

func TestCleanupFailureRetainsAccountStorageReservation(t *testing.T) {
	store := state.NewMemStore()
	account, site := routingSite(t, store, false)
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RetireHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetHostingUsage(t.Context(), account.ID)
	artifacts := &routeArtifacts{removeFailure: true}
	r := Reconciler{Store: store, Artifacts: artifacts, LeasedRoutes: &routeSink{}, AdmissionEnabled: func() bool { return false }, Now: func() time.Time { return time.Now().Add(api.HostingRetirementGrace + time.Second) }}
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	after, _ := store.GetHostingUsage(t.Context(), account.ID)
	if after.ReservedBytes != before.ReservedBytes || after.RetiringReservedBytes == 0 {
		t.Fatal("failed cleanup freed storage")
	}
	artifacts.removeFailure = false
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ = store.GetHostingUsage(t.Context(), account.ID)
	if after.ReservedBytes != 0 || after.Releases != 0 {
		t.Fatalf("successful cleanup retained accounting: %+v", after)
	}
}
