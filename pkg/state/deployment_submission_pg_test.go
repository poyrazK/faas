//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentSubmissionPG(t *testing.T) {
	store, _ := pgStore(t)
	testDeploymentSubmissions(t, store)
}

func TestDeploymentSubmissionPGReceiptFailureRollsBack(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "receipt-rollback@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "receipt-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	// Fault only the receipt insert after the new deployment and supersede writes.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_submission_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt storage fault'; END $$;
	CREATE TRIGGER reject_submission_receipt BEFORE INSERT ON deployment_submission_receipts FOR EACH ROW EXECUTE FUNCTION reject_submission_receipt();`)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 32)
	if _, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, id), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64)}); err == nil {
		t.Fatal("receipt fault allowed a deployment")
	}
	deps, err := store.ListDeploymentsForApp(ctx, app.ID, 10, 0)
	if err != nil || len(deps) != 1 || deps[0].ID != prior.ID || deps[0].Status != state.DeployPending {
		t.Fatalf("rollback deployments=%+v err=%v", deps, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deployment_submission_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipts=%d err=%v", count, err)
	}
}

func TestDeploymentSubmissionPGReceiptPurge(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "receipt-purge@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "receipt-purge"})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("e", 32)
	dep, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, id), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM deployments WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeploymentSubmission(ctx, app.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("purged receipt remained available: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deployment_submission_receipts WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retained receipts=%d err=%v", count, err)
	}
}
