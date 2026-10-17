//go:build !no_pg

package hostingtest

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingHistoryInsertFailureRollsBackPromotion(t *testing.T) {
	// adr: 202610170902 — history failure cannot leave an accepted pointer, marker or outbox.
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, site := seedDurable(t, store)
	ctx := t.Context()
	release, err := store.CreateHostingRelease(ctx, account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	before, err := store.HostingRouteSnapshot(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_hosting_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected history failure'; END $$;
 CREATE TRIGGER reject_hosting_history BEFORE INSERT ON hosting_promotion_events FOR EACH ROW EXECUTE FUNCTION reject_hosting_history();`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PromoteHostingReleaseRecorded(ctx, account.ID, site.ID, release.ID, site.Revision, api.HostingPromotionActor{AccountID: account.ID}); err == nil {
		t.Fatal("injected history failure ignored")
	}
	after, err := store.HostingRouteSnapshot(ctx, site.ID)
	if err != nil || after.Generation != before.Generation || after.Site.Revision != site.Revision || after.Site.ActiveReleaseID != "" {
		t.Fatal("partial promotion", after, err)
	}
	release, err = store.GetHostingRelease(ctx, account.ID, site.ID, release.ID)
	if err != nil || release.FirstPromotedAt != nil {
		t.Fatal("promotion marker survived rollback", err)
	}
	page, err := store.ListHostingHistory(ctx, account.ID, site.ID, 0)
	if err != nil || len(page.Events) != 0 {
		t.Fatal(page, err)
	}
}
