//go:build !no_pg

package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestPgRegionalSourceReferences(t *testing.T) {
	testRegionalSourceReferences(t, state.NewPgStore(openMigratedRegionalPool(t)))
}

func TestPgRegionalSourceReplayPreservesLaterPlacementGuards(t *testing.T) {
	pool := openMigratedRegionalPool(t)
	store := state.NewPgStore(pool)
	fx := conformance.Seed(t, store)
	for _, id := range []string{"eu-project-source", "eu-job-source"} {
		if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, Slug: "source-replay", ComputeRegion: "eu-project-source", ScanSource: state.ProjectScanSourceCompose, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobCreateScheduledIfUnderQuota(fx.Ctx, state.Job{AccountID: fx.Account.ID, Name: "source-replay", Kind: "batch", ImageRef: "registry.example/jobs:v1", Command: []string{"/bin/true"}, RAMMB: 128, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1, ComputeRegion: "eu-job-source"}, 10); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261002002600383_compute_region_registry.sql", "20261002002600387_project_compute_region.sql"} {
		text, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(text), "-- +goose Down")
		if _, err := pool.Exec(fx.Ctx, up); err != nil {
			t.Fatalf("replay %s: %v", name, err)
		}
		for _, id := range []string{"eu-project-source", "eu-job-source"} {
			_, err := pool.Exec(fx.Ctx, "DELETE FROM compute_regions WHERE id=$1", id)
			requireRegionConstraint(t, err, "compute_region_in_use")
		}
	}
}

func TestPgRegionalSourceDirectWriteGuards(t *testing.T) {
	pool := openMigratedRegionalPool(t)
	store := state.NewPgStore(pool)
	fx := conformance.Seed(t, store)
	key := "sources/" + uuid.NewString() + ".tar.gz"
	dep, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindTarball, SourceStorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE deployments SET source_storage_key=$2 WHERE id=$1", dep.ID, "sources/"+uuid.NewString()+".tar.gz")
	requireRegionConstraint(t, err, "deployments_source_reference_immutable")
	if _, err := pool.Exec(fx.Ctx, "UPDATE deployments SET source_storage_key=source_storage_key WHERE id=$1", dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: "eu-source", DisplayName: "Sources", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "source-"+uuid.NewString(), "", "eu-source"
	app, err = store.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "INSERT INTO deployments (app_id, image_digest, status, kind, source_storage_key) VALUES ($1,'','pending','tarball',$2)", app.ID, key)
	requireRegionConstraint(t, err, "deployments_source_compute_region")
	_, err = pool.Exec(fx.Ctx, "UPDATE deployments SET app_id=$2 WHERE id=$1", dep.ID, app.ID)
	requireRegionConstraint(t, err, "deployments_source_compute_region")
	_, err = pool.Exec(fx.Ctx, "INSERT INTO deployments (app_id, image_digest, status, kind, source_storage_key) VALUES ($1,'','pending','tarball',$2)", app.ID, region.ArtifactKey(app.ComputeRegion, "sources/not-a-uuid.tar.gz"))
	requireRegionConstraint(t, err, "deployments_source_storage_key_shape")
	// Rows created before reference persistence keep their old spelling.
	if _, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball}); err != nil {
		t.Fatalf("historical empty reference was rejected: %v", err)
	}
}
