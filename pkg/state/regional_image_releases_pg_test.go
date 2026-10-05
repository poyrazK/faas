//go:build !no_pg

package state

// adr: 604

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func releasePGFixture(t *testing.T) (*PgStore, *pgxpool.Pool, string, Account) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	cellID := uuid.NewString()
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: uuid.MustParse(cellID), Valid: true}, Region: "eu-fsn"}); err != nil {
		t.Fatal(err)
	}
	store := NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "release-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return store, pool, cellID, account
}

func TestPGRegionalImageReleaseContract(t *testing.T) {
	store, _, cellID, account := releasePGFixture(t)
	runRegionalImageReleaseContract(t, store, store, cellID, account)
}

func TestPGRegionalImageReleaseGuardsAndCollection(t *testing.T) {
	store, pool, cellID, account := releasePGFixture(t)
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(cellID, app)
	first, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE regional_image_release_receipts SET image_ref = 'different'`,
		`UPDATE regional_image_release_receipts SET release_id = gen_random_uuid()`,
		`DELETE FROM regional_image_release_receipts`,
		`TRUNCATE regional_image_release_receipts`,
	} {
		if _, err := pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("identity guard allowed %s", statement)
		}
	}
	bytes, err := migrations.FS.ReadFile("20261003223631327_regional_image_release_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(bytes), "-- +goose Down")
	if !ok {
		t.Fatal("migration has no downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("populated downgrade: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM deployments WHERE id=$1`, first.Receipt.DeploymentID); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.RegionalImageReleaseReceipt(t.Context(), cellID, app.ID, intent.ReleaseID)
	if err != nil || receipt.DeploymentID != "" || receipt.DeploymentStatus != "expired" {
		t.Fatalf("collected receipt: %+v %v", receipt, err)
	}
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrRegionalImageReleaseExpired) {
		t.Fatalf("collected retry: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM apps WHERE id=$1`, app.ID); err != nil {
		t.Fatalf("hard app cleanup: %v", err)
	}
	if _, err := store.RegionalImageReleaseReceipt(t.Context(), cellID, app.ID, intent.ReleaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt survived hard app cleanup: %v", err)
	}
	if _, err := pool.Exec(t.Context(), regionalImageReleaseOrderingDownSQL(t)); err != nil {
		t.Fatalf("empty ordering downgrade: %v", err)
	}
	if _, err := pool.Exec(t.Context(), down); err != nil {
		t.Fatalf("empty downgrade: %v", err)
	}
}

func TestPGRegionalImageReleaseAtomicRollback(t *testing.T) {
	store, pool, cellID, account := releasePGFixture(t)
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(cellID, app)
	prior, err := store.CreateDeployment(t.Context(), dep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_release_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$; CREATE TRIGGER reject_release_test BEFORE INSERT ON regional_image_release_receipts FOR EACH ROW EXECUTE FUNCTION reject_release_test()`); err != nil {
		t.Fatal(err)
	}
	activity := releaseTestActivity(app)
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, activity); err == nil || !strings.Contains(err.Error(), "injected receipt failure") {
		t.Fatalf("receipt failure did not roll back after activity enqueue: %v", err)
	}
	rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != prior.ID || rows[0].Status != DeployPending {
		t.Fatalf("deployment rollback: %+v %v", rows, err)
	}
	var receipts, events int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM regional_image_release_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || events != 0 {
		t.Fatalf("partial transaction: receipts=%d events=%d", receipts, events)
	}
	wrong := intent
	wrong.CellID = uuid.NewString()
	if _, err := store.CreateRegionalImageRelease(t.Context(), wrong, dep, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign cell accepted: %v", err)
	}
}

func TestPGRegionalImageReleaseOwnerChange(t *testing.T) {
	store, pool, cellID, account := releasePGFixture(t)
	app := releaseTestApp(t, store, account)
	intent, dep := releaseTestIntent(cellID, app)
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(t.Context(), "new-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE apps SET account_id=$1,org_id=NULL WHERE id=$2`, owner.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegionalImageReleaseReceipt(t.Context(), cellID, app.ID, intent.ReleaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("previous owner's receipt disclosed: %v", err)
	}
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale owner accepted: %v", err)
	}
	intent.AccountID, intent.OrgID = owner.ID, ""
	if _, err := store.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new owner rebound previous receipt: %v", err)
	}
}
