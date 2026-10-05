package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestMemRegionalBuildExports(t *testing.T) { testRegionalBuildExports(t, state.NewMemStore()) }

func testRegionalBuildExports(t *testing.T, s state.Store) {
	fx := conformance.Seed(t, s)
	id := "eu-export"
	if _, err := s.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.Region = &id
	node, err := s.UpsertComputeNodeFromOperator(fx.Ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	peer := node
	peer.ID, peer.Name = "", "image-peer-"+uuid.NewString()
	peer, err = s.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "export-"+uuid.NewString(), "", id
	app, err = s.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy conversion and durable preparation must serialize against the
	// same activation lock even when workers expose different store interfaces.
	activation := s.(state.DeploymentActivationLocker).AcquireDeploymentActivationLock
	materialization := s.(state.BuildImageMaterializationLocker).AcquireBuildImageMaterializationLock
	locks := []func(context.Context, string) (func(context.Context), error){activation, materialization}
	for i, acquire := range locks {
		release, err := acquire(fx.Ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		probeCtx, cancel := context.WithTimeout(fx.Ctx, 100*time.Millisecond)
		contender, err := locks[1-i](probeCtx, dep.ID)
		cancel()
		if contender != nil {
			contender(fx.Ctx)
		}
		release(fx.Ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("image and activation locks allowed overlap: %v", err)
		}
	}
	b, err := s.CreateBuild(fx.Ctx, dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.(state.BuildAffinityClaimStore).ClaimQueuedBuildWithNodeAffinity(fx.Ctx, b.ID, node.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	ref := state.BuildArtifactReference{Key: region.BuildExportKey(id, pub.String(), strings.Repeat("a", 64)), SHA256: strings.Repeat("a", 64), Bytes: 42}
	prov := state.BuildProvenance{BuildID: b.ID, BuilderNodeID: node.Name, Plan: "pro", StartedAt: claim.StartedAt, FinishedAt: time.Now()}
	finish := s.(state.BuildArtifactCompletionStore)
	if err := s.CompleteBuild(fx.Ctx, claim, "/producer/image.tar", "runtime-key", 42, prov); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("regional local-only completion: %v", err)
	}
	for _, bad := range []state.BuildArtifactReference{
		{}, {Key: strings.Replace(ref.Key, id, "us-export", 1), SHA256: ref.SHA256, Bytes: 42},
		{Key: ref.Key, SHA256: strings.Repeat("b", 64), Bytes: 42}, {Key: ref.Key, SHA256: ref.SHA256, Bytes: 43},
	} {
		if err := finish.CompleteBuildWithArtifact(fx.Ctx, claim, "/producer/image.tar", "runtime-key", 42, prov, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid reference accepted: %+v %v", bad, err)
		}
	}
	badProvenance := prov
	badProvenance.SBOMStorageKey = region.BuildSBOMKey("us-export", b.ID)
	if err := finish.CompleteBuildWithArtifact(fx.Ctx, claim, "/producer/image.tar", "runtime-key", 42, badProvenance, ref); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("completion accepted wrong-region SBOM: %v", err)
	}
	stale := claim
	stale.StartedAt = stale.StartedAt.Add(-time.Second)
	if err := finish.CompleteBuildWithArtifact(fx.Ctx, stale, "/stale/image.tar", "stale-key", 42, prov, ref); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion: %v", err)
	}
	before, _ := s.DeploymentByID(fx.Ctx, dep.ID)
	if before.BuildArtifactKey != "" || before.RootfsPath != "" {
		t.Fatal("rejected completion wrote metadata")
	}
	if err := finish.CompleteBuildWithArtifact(fx.Ctx, claim, "/producer/image.tar", "runtime-key", 42, prov, ref); err != nil {
		t.Fatal(err)
	}
	for _, getter := range []func() (state.Deployment, error){
		func() (state.Deployment, error) { return s.DeploymentByID(fx.Ctx, dep.ID) },
		func() (state.Deployment, error) { return s.LatestDeployment(fx.Ctx, app.ID) },
	} {
		d, err := getter()
		if err != nil || d.BuildArtifactReference() != ref {
			t.Fatalf("lost export projection: %+v %v", d, err)
		}
	}
	if err := s.SetDeploymentRootfs(fx.Ctx, dep.ID, "/consumer/final.ext4", "final-key", 7); err != nil {
		t.Fatal(err)
	}
	d, _ := s.DeploymentByID(fx.Ctx, dep.ID)
	if d.BuildArtifactReference() != ref {
		t.Fatal("runtime rootfs replaced build export")
	}
	// Pausing new admission must not strand already admitted deployments.
	if _, err := s.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: false}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployPending, state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err := s.UpdateDeploymentStatus(fx.Ctx, dep.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		work, err := s.ListBuildsAwaitingImage(fx.Ctx, peer.Name, 16)
		if err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
			t.Fatalf("peer recovery in %s: %+v %v", status, work, err)
		}
		active, err := s.(state.BuildArtifactReferenceStore).BuildArtifactReferencedForMaterialization(fx.Ctx, ref.Key)
		if err != nil || !active {
			t.Fatalf("active reference lost: %v %v", active, err)
		}
	}
	for _, name := range []string{"", "unknown-worker", state.DefaultLocalNodeName} {
		work, err := s.ListBuildsAwaitingImage(fx.Ctx, name, 16)
		if err != nil || len(work) != 0 {
			t.Fatalf("foreign/unnamed recovery %q: %+v %v", name, work, err)
		}
	}
	if err := s.SetComputeNodeActive(fx.Ctx, peer.ID, false); err != nil {
		t.Fatal(err)
	}
	if work, err := s.ListBuildsAwaitingImage(fx.Ctx, peer.Name, 16); err != nil || len(work) != 0 {
		t.Fatalf("inactive recovery: %+v %v", work, err)
	}

	images := s.(state.DeploymentImagePreparationStore)
	if err := s.UpdateDeploymentStatus(fx.Ctx, dep.ID, state.DeployBuilding, ""); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := images.BeginImagePreparation(fx.Ctx, dep.ID, "")
	if err != nil || checkpoint.InputPath != "sha256:"+ref.SHA256 || checkpoint.InputKey != ref.Key || checkpoint.InputBytes != ref.Bytes || checkpoint.NodeName != "" {
		t.Fatalf("regional preparation lost immutable export: %+v %v", checkpoint, err)
	}
	for _, name := range []string{peer.Name, "unknown-worker", state.DefaultLocalNodeName} {
		if work, err := images.ListResumableImagePreparations(fx.Ctx, name, 16); err != nil || len(work) != 0 {
			t.Fatalf("inactive/foreign preparation recovery %q: %+v %v", name, work, err)
		}
	}
	if err := s.SetComputeNodeActive(fx.Ctx, peer.ID, true); err != nil {
		t.Fatal(err)
	}
	if work, err := images.ListResumableImagePreparations(fx.Ctx, peer.Name, 16); err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
		t.Fatalf("eligible regional preparation recovery: %+v %v", work, err)
	}
	if err := images.TransitionImagePreparation(fx.Ctx, dep.ID, checkpoint.ClaimToken, state.DeployImaging); err != nil {
		t.Fatal(err)
	}
	if err := images.PublishImagePreparationLayer(fx.Ctx, dep.ID, checkpoint.ClaimToken, "/consumer/final.ext4", region.ArtifactKey(id, "apps/final.ext4"), 7); err != nil {
		t.Fatal(err)
	}
	resumed, err := images.BeginImagePreparation(fx.Ctx, dep.ID, "")
	if err != nil || resumed.Phase != state.ImageLayerPublished || resumed.InputPath != checkpoint.InputPath || resumed.InputKey != checkpoint.InputKey || resumed.InputBytes != checkpoint.InputBytes || resumed.ClaimToken == checkpoint.ClaimToken {
		t.Fatalf("regional checkpoint lost published layer or input: %+v %v", resumed, err)
	}
	if err := s.UpdateDeploymentStatus(fx.Ctx, dep.ID, state.DeployCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if active, err := s.(state.BuildArtifactReferenceStore).BuildArtifactReferencedForMaterialization(fx.Ctx, ref.Key); err != nil || active {
		t.Fatalf("terminal retention: %v %v", active, err)
	}
	if err := s.(state.BuildImageStateStore).AdvanceBuildImageStatus(fx.Ctx, dep.ID, state.DeployImaging, state.DeploySnapshotting); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("cancelled deployment advanced: %v", err)
	}
}
