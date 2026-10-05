//go:build !no_pg

package state

// adr: 605

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalImageReleaseOrderingDownSQL(t *testing.T) string {
	t.Helper()
	b, err := migrations.FS.ReadFile("20261004002410319_regional_image_release_ordering.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("ordering migration has no downgrade")
	}
	return down
}

func TestPGRegionalImageReleaseOrdering(t *testing.T) {
	store, _, cellID, account := releasePGFixture(t)
	runRegionalImageReleaseOrdering(t, store, store, cellID, account)
}

func TestPGRegionalImageReleaseOrderingGuardsAndCollection(t *testing.T) {
	store, pool, cellID, account := releasePGFixture(t)
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(cellID, app)
	intent.GlobalAppID, intent.Generation = uuid.NewString(), 3
	first, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE regional_image_release_heads SET generation = 2`,
		`UPDATE regional_image_release_heads SET generation = 4`,
		`UPDATE regional_image_release_heads SET global_app_id = gen_random_uuid()`,
		`DELETE FROM regional_image_release_heads`,
		`TRUNCATE regional_image_release_heads`,
		`UPDATE regional_image_release_receipts SET global_app_id = NULL, generation = NULL`,
		`UPDATE regional_image_release_receipts SET generation = 4`,
	} {
		if _, err := pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("SQL ordering guard allowed %s", statement)
		}
	}
	if _, err := pool.Exec(t.Context(), regionalImageReleaseOrderingDownSQL(t)); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("populated downgrade: %v", err)
	}
	// A SQL client can insert valid deployment intent, but cannot bypass the
	// generation fence when attaching its receipt. The failed insert is atomic.
	if _, err := pool.Exec(t.Context(), `INSERT INTO regional_image_release_receipts
		(app_id,release_id,cell_id,account_id,image_ref,deployment_id,global_app_id,generation)
		VALUES ($1,$2,$3,$4,$5,$6,$7,2)`, app.ID, uuid.NewString(), cellID, account.ID, intent.ImageRef, first.Receipt.DeploymentID, intent.GlobalAppID); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("SQL stale receipt: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM deployments WHERE id=$1`, first.Receipt.DeploymentID); err != nil {
		t.Fatal(err)
	}
	retry, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
	if !errors.Is(err, ErrRegionalImageReleaseExpired) {
		t.Fatalf("collected ordered retry: %+v %v", retry, err)
	}
	stale := intent
	stale.ReleaseID, stale.Generation = uuid.NewString(), 2
	if _, err := store.CreateRegionalImageRelease(t.Context(), stale, dep, nil); !errors.Is(err, ErrRegionalImageReleaseOrder) {
		t.Fatalf("collection reset generation fence: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM apps WHERE id=$1`, app.ID); err != nil {
		t.Fatalf("hard app cleanup: %v", err)
	}
	var heads int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM regional_image_release_heads`).Scan(&heads); err != nil || heads != 0 {
		t.Fatalf("hard app deletion retained heads: %d %v", heads, err)
	}
	if _, err := pool.Exec(t.Context(), regionalImageReleaseOrderingDownSQL(t)); err != nil {
		t.Fatalf("empty downgrade: %v", err)
	}
}

func TestPGRegionalImageReleaseOrderingAtomicRollback(t *testing.T) {
	store, pool, cellID, account := releasePGFixture(t)
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(cellID, app)
	intent.GlobalAppID, intent.Generation = uuid.NewString(), 1
	first, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_ordered_receipt_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected after head advance'; END $$;
		CREATE TRIGGER zz_reject_ordered_receipt_test AFTER INSERT ON regional_image_release_receipts FOR EACH ROW EXECUTE FUNCTION reject_ordered_receipt_test()`); err != nil {
		t.Fatal(err)
	}
	newer := intent
	newer.ReleaseID, newer.Generation = uuid.NewString(), 2
	if _, err := store.CreateRegionalImageRelease(t.Context(), newer, dep, releaseTestActivity(app)); err == nil || !strings.Contains(err.Error(), "injected after head advance") {
		t.Fatalf("head failure: %v", err)
	}
	head, err := (&sqlc.Queries{}).RegionalImageReleaseHead(t.Context(), pool, uuidToPgtype(app.ID))
	if err != nil || head.Generation != 1 {
		t.Fatalf("partial head: %+v %v", head, err)
	}
	current, err := store.DeploymentByID(t.Context(), first.Receipt.DeploymentID)
	if err != nil || current.Status != DeployPending {
		t.Fatalf("supersession did not roll back: %+v %v", current, err)
	}
	var events int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("partial activity: %d %v", events, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER zz_reject_ordered_receipt_test ON regional_image_release_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRegionalImageRelease(t.Context(), newer, dep, nil); err != nil {
		t.Fatalf("rolled back generation poisoned retries: %v", err)
	}
}
