//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgRegionalBuildSBOMs(t *testing.T) {
	testRegionalBuildSBOMs(t, state.NewPgStore(openMigratedRegionalPool(t)))
}

func TestPgRegionalBuildSBOMDirectWriteGuards(t *testing.T) {
	pool := openMigratedRegionalPool(t)
	s := state.NewPgStore(pool)
	app, dep, build, _ := seedRegionalSBOM(t, s)
	for _, bad := range []string{region.BuildSBOMKey("", build.ID), region.BuildSBOMKey("us-sbom", build.ID), region.BuildSBOMKey(app.ComputeRegion, app.ID)} {
		_, err := pool.Exec(t.Context(), "UPDATE build_provenance SET sbom_storage_key=$1 WHERE build_id=$2", bad, build.ID)
		requireRegionConstraint(t, err, "build_provenance_sbom_region")
	}
	want := region.BuildSBOMKey(app.ComputeRegion, build.ID)
	if err := s.UpdateBuildProvenanceSBOM(t.Context(), build.ID, want); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: dep.Kind})
	if err != nil {
		t.Fatal(err)
	}
	otherBuild, err := s.CreateBuild(t.Context(), other.ID, dep.Kind, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{region.BuildSBOMKey("", otherBuild.ID), region.BuildSBOMKey("us-sbom", otherBuild.ID), region.BuildSBOMKey(app.ComputeRegion, build.ID)} {
		_, err := pool.Exec(t.Context(), "INSERT INTO build_provenance(build_id, source_sha256, started_at, finished_at, sbom_storage_key) VALUES($1, '', now(), now(), $2)", otherBuild.ID, bad)
		requireRegionConstraint(t, err, "build_provenance_sbom_region")
	}
	_, err = pool.Exec(t.Context(), "UPDATE builds SET deployment_id=$1 WHERE id=$2", other.ID, build.ID)
	requireRegionConstraint(t, err, "builds_sbom_identity_immutable")
	_, err = pool.Exec(t.Context(), "UPDATE build_provenance SET build_id=$1 WHERE build_id=$2", otherBuild.ID, build.ID)
	requireRegionConstraint(t, err, "build_provenance_sbom_region")
	target := app
	target.ID, target.Slug = "", app.Slug+"-other"
	target, err = s.CreateApp(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), "UPDATE deployments SET app_id=$1 WHERE id=$2", target.ID, dep.ID)
	requireRegionConstraint(t, err, "deployments_sbom_identity_immutable")
}
