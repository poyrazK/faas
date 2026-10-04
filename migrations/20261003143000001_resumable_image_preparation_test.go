//go:build !no_pg

package migrations_test

import (
	"context"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestResumableImagePreparationMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "image-migration@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "image-migration", Type: state.AppTypeApp, RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example.test/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261003143000001_resumable_image_preparation.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginImagePreparation(ctx, dep.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployment_image_preparations SET phase='unknown' WHERE deployment_id=$1", dep.ID); err == nil {
		t.Fatal("unknown phase accepted")
	}
	if _, err := pool.Exec(ctx, "UPDATE deployment_image_preparations SET input_path='' WHERE deployment_id=$1", dep.ID); err == nil {
		t.Fatal("empty input accepted")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || got.Status != state.DeployPending || got.ImageDigest != dep.ImageDigest {
		t.Fatalf("rollback modified deployment: %+v %v", got, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginImagePreparation(ctx, dep.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM deployments WHERE id=$1", dep.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM deployment_image_preparations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned checkpoint: %d %v", count, err)
	}
}
