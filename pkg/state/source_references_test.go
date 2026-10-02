package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestMemRegionalSourceReferences(t *testing.T) {
	testRegionalSourceReferences(t, state.NewMemStore())
}

func testRegionalSourceReferences(t *testing.T, store state.Store) {
	t.Helper()
	fx := conformance.Seed(t, store)
	if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: "eu-source", DisplayName: "Sources", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "source-"+uuid.NewString(), "", "eu-source"
	var err error
	app, err = store.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	key := region.ArtifactKey(app.ComputeRegion, "sources/"+uuid.NewString()+".tar.gz")
	for _, invalid := range []string{
		strings.TrimPrefix(key, "regions/eu-source/"),
		strings.Replace(key, "eu-source", "us-source", 1),
		"regions/eu-source/sources/not-a-uuid.tar.gz",
		"regions/eu-source/sources/../" + uuid.NewString() + ".tar.gz",
	} {
		if _, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceStorageKey: invalid}); err == nil {
			t.Fatalf("accepted invalid source reference %q", invalid)
		}
	}
	original, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceStorageKey: key, SourceBytes: 16, SourceSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	refs := store.(state.SourceArtifactReferenceStore)
	if live, err := refs.SourceArtifactReferencedByPendingBuild(fx.Ctx, key); err != nil || !live {
		t.Fatalf("pre-queue reference: %v %v", live, err)
	}
	if err := store.FailSourceDeployment(fx.Ctx, original.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if live, err := refs.SourceArtifactReferencedByPendingBuild(fx.Ctx, key); err != nil || live {
		t.Fatalf("terminal reference: %v %v", live, err)
	}
	retained, err := store.RetryDeploymentFromStage(fx.Ctx, original.ID, state.StageSecurityScan)
	if err != nil || retained.SourceStorageKey != key {
		t.Fatalf("retained retry: %+v %v", retained, err)
	}
	if live, err := refs.SourceArtifactReferencedByPendingBuild(fx.Ctx, key); err != nil || !live {
		t.Fatalf("retained retry reference: %v %v", live, err)
	}
	newKey := region.ArtifactKey(app.ComputeRegion, "sources/"+uuid.NewString()+".tar.gz")
	retry, err := store.(state.SourceRetryStore).RetryDeploymentFromStageWithSource(fx.Ctx, original.ID, state.StageImageBuild, newKey)
	if err != nil || retry.SourceStorageKey != newKey || retry.SourceSHA256 != original.SourceSHA256 {
		t.Fatalf("republished retry lost source identity: %+v %v", retry, err)
	}
	read, err := store.DeploymentByID(fx.Ctx, retry.ID)
	if err != nil || read.SourceStorageKey != newKey {
		t.Fatalf("deployment projection: %+v %v", read, err)
	}
	latest, err := store.LatestDeployment(fx.Ctx, app.ID)
	if err != nil || latest.SourceStorageKey != newKey {
		t.Fatalf("latest projection: %+v %v", latest, err)
	}
	if _, err := store.(state.SourceRetryStore).RetryDeploymentFromStageWithSource(fx.Ctx, original.ID, state.StageImageBuild, strings.Replace(newKey, "eu-source", "us-source", 1)); err == nil {
		t.Fatal("retry bound a source from another region")
	}
	if got := state.BuildSourceKey(state.Deployment{}, "old-build"); got != "sources/old-build.tar.gz" {
		t.Fatalf("historical source key was rewritten: %q", got)
	}
	if got := state.BuildSourceKey(read, "different-build"); got != newKey {
		t.Fatalf("committed source key was rewritten: %q", got)
	}
}
