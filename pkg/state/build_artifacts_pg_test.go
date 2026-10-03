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

func TestPgRegionalBuildExports(t *testing.T) {
	testRegionalBuildExports(t, state.NewPgStore(openMigratedRegionalPool(t)))
}

func TestPgRegionalBuildExportDirectWriteGuards(t *testing.T) {
	pool := openMigratedRegionalPool(t)
	s := state.NewPgStore(pool)
	fx := conformance.Seed(t, s)
	id := "eu-export-guard"
	if _, err := s.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "exports-"+uuid.NewString(), "", id
	app, err := s.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBuild(fx.Ctx, d.ID, d.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE builds SET status='succeeded' WHERE id=$1", b.ID)
	requireRegionConstraint(t, err, "builds_regional_export_required")
	pub, _ := uuid.NewV7()
	digest := strings.Repeat("a", 64)
	key := region.BuildExportKey(id, pub.String(), digest)
	for _, bad := range []struct {
		key, hash, constraint string
		bytes                 int64
	}{
		{strings.Replace(key, id, "us-export", 1), digest, "deployments_build_artifact_region", 42},
		{key, strings.Repeat("b", 64), "deployments_build_artifact_shape", 42},
		{key, digest, "deployments_build_artifact_shape", 0},
		{strings.Replace(key, pub.String(), uuid.NewString(), 1), digest, "deployments_build_artifact_shape", 42},
	} {
		_, err = pool.Exec(fx.Ctx, "UPDATE deployments SET build_artifact_key=$2,build_artifact_sha256=$3,build_artifact_bytes=$4 WHERE id=$1", d.ID, bad.key, bad.hash, bad.bytes)
		requireRegionConstraint(t, err, bad.constraint)
	}
	if _, err = pool.Exec(fx.Ctx, "UPDATE deployments SET build_artifact_key=$2,build_artifact_sha256=$3,build_artifact_bytes=42 WHERE id=$1", d.ID, key, digest); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE deployments SET build_artifact_key='',build_artifact_sha256='',build_artifact_bytes=0 WHERE id=$1",
		"UPDATE deployments SET build_artifact_bytes=43 WHERE id=$1",
	} {
		_, err = pool.Exec(fx.Ctx, query, d.ID)
		requireRegionConstraint(t, err, "deployments_build_artifact_immutable")
	}
	sibling := app
	sibling.ID, sibling.Slug = "", "export-sibling-"+uuid.NewString()
	sibling, err = s.CreateApp(fx.Ctx, sibling)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE deployments SET app_id=$2 WHERE id=$1", d.ID, sibling.ID)
	requireRegionConstraint(t, err, "deployments_build_artifact_immutable")
	// Replaying Up over a populated schema preserves both metadata and guards.
	raw, err := migrations.FS.ReadFile("20261002013759246_regional_build_exports.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(fx.Ctx, up); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE deployments SET build_artifact_bytes=43 WHERE id=$1", d.ID)
	requireRegionConstraint(t, err, "deployments_build_artifact_immutable")
}

// The descriptor, success status and provenance must roll back together.
func TestPgRegionalBuildExportProvenanceRollback(t *testing.T) {
	pool := openMigratedRegionalPool(t)
	s := state.NewPgStore(pool)
	fx := conformance.Seed(t, s)
	id := "eu-export-rollback"
	if _, err := s.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.Region = &id
	node, err := s.UpsertComputeNodeFromOperator(fx.Ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "rollback-"+uuid.NewString(), "", id
	app, err = s.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBuild(fx.Ctx, dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, node.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(fx.Ctx, `CREATE FUNCTION reject_export_provenance() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected provenance failure';END$$;
CREATE TRIGGER reject_export_provenance BEFORE INSERT ON build_provenance FOR EACH ROW EXECUTE FUNCTION reject_export_provenance()`); err != nil {
		t.Fatal(err)
	}
	pub, _ := uuid.NewV7()
	hash := strings.Repeat("a", 64)
	ref := state.BuildArtifactReference{Key: region.BuildExportKey(id, pub.String(), hash), SHA256: hash, Bytes: 42}
	err = s.CompleteBuildWithArtifact(fx.Ctx, claim, "/producer/image.tar", "runtime-key", 42, state.BuildProvenance{BuildID: b.ID, Plan: "pro"}, ref)
	if err == nil {
		t.Fatal("expected provenance failure")
	}
	d, err := s.DeploymentByID(fx.Ctx, dep.ID)
	if err != nil || d.BuildArtifactReference() != (state.BuildArtifactReference{}) || d.RootfsPath != "" {
		t.Fatalf("partial artifact: %+v %v", d, err)
	}
	row, err := s.BuildByID(fx.Ctx, b.ID)
	if err != nil || row.Status != state.BuildRunning {
		t.Fatalf("partial success: %+v %v", row, err)
	}
}
