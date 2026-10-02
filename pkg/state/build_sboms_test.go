package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestMemRegionalBuildSBOMs(t *testing.T) { testRegionalBuildSBOMs(t, state.NewMemStore()) }

func seedRegionalSBOM(t *testing.T, s state.Store) (state.App, state.Deployment, state.Build, state.BuildProvenance) {
	t.Helper()
	fx := conformance.Seed(t, s)
	id := "eu-sbom"
	if _, err := s.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "sbom-"+uuid.NewString(), "", id
	app, err := s.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	build, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	prov := state.BuildProvenance{BuildID: build.ID, SourceSHA256: strings.Repeat("a", 64), StartedAt: time.Now(), FinishedAt: time.Now()}
	if err := s.CreateBuildProvenance(t.Context(), prov); err != nil {
		t.Fatal(err)
	}
	return app, dep, build, prov
}

func testRegionalBuildSBOMs(t *testing.T, s state.Store) {
	app, _, build, prov := seedRegionalSBOM(t, s)
	want := region.BuildSBOMKey(app.ComputeRegion, build.ID)
	if err := s.UpdateBuildProvenanceSBOM(t.Context(), build.ID, want); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		region.BuildSBOMKey("", build.ID), region.BuildSBOMKey("us-sbom", build.ID),
		region.BuildSBOMKey(app.ComputeRegion, uuid.NewString()),
		"regions/" + app.ComputeRegion + "/sboms/../sboms/" + build.ID + ".cdx.json",
		"regions/" + app.ComputeRegion + "/apps/other.ext4",
	} {
		if err := s.UpdateBuildProvenanceSBOM(t.Context(), build.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("update accepted %q: %v", bad, err)
		}
		prov.SBOMStorageKey = bad
		if err := s.CreateBuildProvenance(t.Context(), prov); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("upsert accepted %q: %v", bad, err)
		}
	}
	if err := s.UpdateBuildProvenanceSBOM(t.Context(), build.ID, ""); err != nil {
		t.Fatal(err)
	}
	prov.SBOMStorageKey = ""
	if err := s.CreateBuildProvenance(t.Context(), prov); err != nil {
		t.Fatal(err)
	}
	got, err := s.BuildProvenanceByBuildID(t.Context(), build.ID)
	if err != nil || got.SBOMStorageKey != want {
		t.Fatalf("retry lost reference: %+v %v", got, err)
	}
	refs, err := s.ListAppDeletionArtifacts(t.Context(), app.ID)
	if err != nil || len(refs) != 1 || refs[0].Key != want {
		t.Fatalf("deletion lost regional key: %+v %v", refs, err)
	}
	fx := conformance.Seed(t, s)
	legacyDep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	legacyBuild, err := s.CreateBuild(t.Context(), legacyDep.ID, legacyDep.Kind, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	prov.BuildID = legacyBuild.ID
	prov.SBOMStorageKey = region.BuildSBOMKey(app.ComputeRegion, legacyBuild.ID)
	if err := s.CreateBuildProvenance(t.Context(), prov); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("legacy build acquired a regional reference: %v", err)
	}
}
