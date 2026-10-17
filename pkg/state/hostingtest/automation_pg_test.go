//go:build !no_pg

package hostingtest

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingCIRunFailureRollsBackReleaseAndPreview(t *testing.T) {
	// adr: 202610170925 — run identity and preview marker must commit with their resources.
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, site := seedDurable(t, store)
	ctx := t.Context()
	req := api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_ci_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected CI failure'; END $$;
 CREATE TRIGGER reject_ci_insert BEFORE INSERT ON hosting_ci_runs FOR EACH ROW EXECUTE FUNCTION reject_ci_write();`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, id, req); err == nil {
		t.Fatal("run failure ignored")
	}
	releases, err := store.ListHostingReleases(ctx, account.ID, site.ID)
	if err != nil || len(releases) != 0 {
		t.Fatal("orphan release", releases, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_ci_insert ON hosting_ci_runs`); err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, id, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TRIGGER reject_ci_marker BEFORE UPDATE ON hosting_ci_runs FOR EACH ROW EXECUTE FUNCTION reject_ci_write()`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, id); err == nil {
		t.Fatal("preview marker failure ignored")
	}
	previews, err := store.ListHostingPreviews(ctx, account.ID, site.ID)
	if err != nil || len(previews) != 0 {
		t.Fatal("orphan preview", previews, err)
	}
	retained, err := store.GetHostingCIRun(ctx, account.ID, site.ID, id)
	if err != nil || retained.PreviewCreated {
		t.Fatal("marker survived rollback", retained, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_ci_marker ON hosting_ci_runs`); err != nil {
		t.Fatal(err)
	}
	preview, err := store.CreateHostingCIPreview(ctx, account.ID, site.ID, id)
	if err != nil || preview.ID != run.PreviewID {
		t.Fatal(preview, err)
	}
}

func TestHostingCIRunQuotaPreservesExistingIntent(t *testing.T) {
	// adr: 202610170925 — retained identities are bounded and never pruned to admit retries.
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, site := seedDurable(t, store)
	ctx := t.Context()
	req := api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600}
	run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_ci_runs(site_id,id,release_id,preview_id,fingerprint,preview_expires_at)
 SELECT $1,gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),repeat('a',64),clock_timestamp()+interval '1 hour' FROM generate_series(1,$2::integer)`, site.ID, api.HostingCIRunsPerSite-1); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), req); !errors.Is(err, state.ErrHostingQuota) {
		t.Fatal("unbounded run ledger", err)
	}
	retry, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, run.ID, req)
	if err != nil || retry.ReleaseID != run.ReleaseID {
		t.Fatal("quota broke retry", retry, err)
	}
	releases, err := store.ListHostingReleases(ctx, account.ID, site.ID)
	if err != nil || len(releases) != 1 {
		t.Fatal("rejected quota allocated release", releases, err)
	}
}
