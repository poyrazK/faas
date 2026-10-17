package hostingtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

func stagedReleaseVisibility(t *testing.T, store hostingTestStore) {
	// adr: 202610161920 — verified releases become routable only on successful CAS promotion.
	account, site := seedDurable(t, store)
	old, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	site, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, old.ID, site.Revision)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := store.GetHostingRelease(t.Context(), account.ID, site.ID, old.ID)
	if err != nil || previous.FirstPromotedAt == nil {
		t.Fatal("promotion marker missing", err)
	}
	stage, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	stage, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, stage.ID)
	if err != nil || stage.FirstPromotedAt != nil {
		t.Fatal("verification marked release promoted", err)
	}
	snapshot, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	head := hosting.NewDurableRoute(snapshot.Site, snapshot.Releases, snapshot.Generation)
	if head.ActiveReleaseID != old.ID || !slices.Equal(head.ReadyReleases, []string{old.ID}) || snapshot.Site.Revision != site.Revision {
		t.Fatalf("staging altered live inventory: %+v", head)
	}
	inventory, err := store.ListHostingReleases(t.Context(), account.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	leased := hosting.NewRoute(site, inventory, true, time.Now())
	if !slices.Equal(leased.ReadyReleases, []string{old.ID}) {
		t.Fatal("legacy routing exposed staged release")
	}
	if _, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, stage.ID, site.Revision-1); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	stage, err = store.GetHostingRelease(t.Context(), account.ID, site.ID, stage.ID)
	if err != nil || stage.FirstPromotedAt != nil {
		t.Fatal("failed promotion leaked marker", err)
	}
	site, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, stage.ID, site.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	head = hosting.NewDurableRoute(snapshot.Site, snapshot.Releases, snapshot.Generation)
	if len(head.ReadyReleases) != 2 || !slices.Contains(head.ReadyReleases, stage.ID) {
		t.Fatal("successful promotion missing retained inventory")
	}
	if _, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, old.ID, site.Revision); err != nil {
		t.Fatal(err)
	}
	rollback, err := store.GetHostingRelease(t.Context(), account.ID, site.ID, old.ID)
	if err != nil || !rollback.FirstPromotedAt.Equal(*previous.FirstPromotedAt) {
		t.Fatal("rollback changed first promotion", err)
	}
	*rollback.FirstPromotedAt = time.Time{}
	reread, err := store.GetHostingRelease(t.Context(), account.ID, site.ID, old.ID)
	if err != nil || !reread.FirstPromotedAt.Equal(*previous.FirstPromotedAt) {
		t.Fatal("caller mutated promotion marker", err)
	}
}
