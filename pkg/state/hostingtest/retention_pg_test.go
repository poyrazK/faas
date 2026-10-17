//go:build !no_pg

package hostingtest

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingRetentionActivityAndOutboxRollback(t *testing.T) {
	// adr: 202610171001 — new activity invalidates a prior plan; failed outbox writes retire nothing.
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, site := seedDurable(t, store)
	ctx := t.Context()
	first, err := store.CreateHostingRelease(ctx, account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostingRelease(ctx, account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetHostingRetentionPolicy(ctx, account.ID, site.ID, api.SetHostingRetentionRequest{Enabled: true, HostingRetentionRules: hosting.DefaultRetentionRules()}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_releases SET created_at=clock_timestamp()-interval '2 days',last_activity_at=clock_timestamp()-interval '2 days' WHERE site_id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := store.GetHostingCleanupPlan(ctx, account.ID, site.ID, nil, time.Now())
	if err != nil || !plan.Entries[0].Eligible || !plan.Entries[1].Eligible {
		t.Fatal(plan, err)
	}
	touched, err := store.TouchHostingRelease(ctx, account.ID, site.ID, first.ID)
	if err != nil || time.Since(touched.LastActivityAt) > time.Minute {
		t.Fatal(touched, err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_retention_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$;
 CREATE TRIGGER reject_retention_outbox BEFORE INSERT OR UPDATE ON hosting_route_publications FOR EACH ROW EXECUTE FUNCTION reject_retention_outbox();`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, time.Now()); err == nil {
		t.Fatal("outbox failure ignored")
	}
	for _, id := range []string{first.ID, second.ID} {
		r, err := store.GetHostingRelease(ctx, account.ID, site.ID, id)
		if err != nil || r.State != "pending" || r.RetiredAt != nil {
			t.Fatal("partial retirement", r, err)
		}
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_retention_outbox ON hosting_route_publications`); err != nil {
		t.Fatal(err)
	}
	ids, err := store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, time.Now())
	if err != nil || len(ids) != 1 || ids[0] != second.ID {
		t.Fatal("stale plan beat activity", ids, err)
	}
}
