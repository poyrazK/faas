package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type layerRetentionTestStore interface {
	cloneWorkloadTestStore
	state.DeploymentAliasStore
	state.ProjectReleaseSetStore
}

// ADR-568: consumers retain retired deployments and cannot resurrect a deleted
// artifact after their final reference is removed.
func TestMemLayerRetentionConsumerReferences(t *testing.T) {
	layerRetentionConsumerReferences(t, state.NewMemStore())
}

func layerRetentionConsumerReferences(t *testing.T, s layerRetentionTestStore) {
	t.Helper()
	ctx := context.Background()
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []string{"snapshot", "instance", "alias", "release"} {
		t.Run(consumer, func(t *testing.T) {
			a, err := s.CreateAccount(ctx, consumer+"-retention@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: consumer + "-retention"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: consumer + "-retention", Type: state.AppTypeApp,
				Manifest: state.AppManifest{RevisionPinTTLSeconds: 1800}})
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			key := "layers/" + consumer + "-" + d.ID
			if err := s.SetDeploymentRootfs(ctx, d.ID, "/retained.ext4", key, 1024); err != nil {
				t.Fatal(err)
			}
			var snapshot state.Snapshot
			var instance state.Instance
			switch consumer {
			case "snapshot":
				snapshot, err = s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: d.ID, StorageKey: state.SnapMemKey(d.ID), FCVersion: "test"})
			case "instance":
				instance, err = s.CreateInstance(ctx, app.ID, d.ID, string(state.StateRunning), 128, node.ID, "")
			case "alias":
				_, err = s.SetDeploymentAlias(ctx, app.ID, "retained", d.ID)
			case "release":
				if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				_, err = s.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.ClearDeployment(ctx, d.ID, "test"); err != nil {
				t.Fatal(err)
			}
			if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
				t.Fatalf("%s lost layer retention: eligible=%t, err=%v", consumer, eligible, err)
			}
			if bytes, err := s.RetainedLayerBytes(ctx, app.ID); err != nil || bytes != 1024 {
				t.Fatalf("%s retained layer omitted from capacity: %d, %v", consumer, bytes, err)
			}
			if err := s.SetDeploymentRootfs(ctx, d.ID, "/retained.ext4", key, 1024); err != nil {
				t.Fatal(err)
			}
			pending, err := s.PendingLayerArtifactDeletions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, request := range pending {
				found = found || request.StorageKey == key && request.State == state.LayerArtifactRetained && request.DeletionID == ""
			}
			if !found {
				t.Fatal("retained-key publication lost the saved cleanup request")
			}
			switch consumer {
			case "snapshot":
				err = s.MarkSnapshotStale(ctx, snapshot.ID)
			case "instance":
				err = s.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped))
			case "alias":
				err = s.DeleteDeploymentAlias(ctx, app.ID, "retained")
			case "release":
				// Replacing an active graph retains its old members through
				// the compatibility window even after the deployment is cleared.
				newer, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(ctx, newer.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: newer.ID}}); err != nil {
					t.Fatal(err)
				}
				if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
					t.Fatalf("retained release lost layer: %t, %v", eligible, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			claim, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key)
			if err != nil || !eligible {
				t.Fatalf("released %s still retains layer: %t, %v", consumer, eligible, err)
			}
			if err := s.CompleteLayerArtifactDeletion(ctx, claim); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: d.ID, StorageKey: state.WarmSnapMemKey(d.ID), Tier: state.SnapshotTierWarm, FCVersion: "test"}); !errors.Is(err, state.ErrLayerArtifactRetired) {
				t.Fatalf("snapshot revived retired layer: %v", err)
			}
			if _, err := s.CreateInstance(ctx, app.ID, d.ID, string(state.StateColdBooting), 128, node.ID, ""); !errors.Is(err, state.ErrLayerArtifactRetired) {
				t.Fatalf("new VM revived retired layer: %v", err)
			}
			if consumer == "instance" {
				if err := s.UpdateInstanceStateIf(ctx, instance.ID, string(state.StateStopped), string(state.StateColdBooting)); !errors.Is(err, state.ErrLayerArtifactRetired) {
					t.Fatalf("VM restart revived retired layer: %v", err)
				}
			}
		})
	}
}

// ADR-568: source retirement cannot invalidate a captured or prepared stage.
func TestMemCloneLayerRetentionSurvivesSourceRetirement(t *testing.T) {
	cloneLayerRetentionSurvivesSourceRetirement(t, state.NewMemStore())
}

func cloneLayerRetentionSurvivesSourceRetirement(t *testing.T, s layerRetentionTestStore) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, "layer-retention@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "retained"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "retained-api", WorkloadName: "api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:retained",
		Sidecars: []byte(`[{"name":"metrics","type":"sidecar","image":"metrics@sha256:retained"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	mainKey, sidecarKey := "layers/source-"+source.ID, "layers/metrics-"+source.ID
	if err := s.SetDeploymentRootfs(ctx, source.ID, "/source.ext4", mainKey, 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: source.ID, SidecarName: "metrics", StorageKey: sidecarKey, Bytes: 512, ContentDigest: "sha256:metrics"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: a.ID, ProjectID: p.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "layers", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 {
		t.Fatalf("capture: count=%d, err=%v", len(views), err)
	}
	newer, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, newer.ID, "/newer.ext4", "layers/newer-"+newer.ID, 1024); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearDeployment(ctx, source.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{mainKey, sidecarKey} {
		if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
			t.Fatalf("captured layer eligible for deletion: %t, %v", eligible, err)
		}
	}
	if bytes, err := s.RetainedLayerBytes(ctx, app.ID); err != nil || bytes != 5632 {
		t.Fatalf("capture-only physical bytes: %d, %v", bytes, err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: a.ID, ProjectID: p.ID, SourceSlug: "production", TargetSlug: "stage", CloneOperationID: op.ID, CloneOperationRevision: op.Revision}, api.MustLimitsFor(a.Plan)); err != nil {
		t.Fatal(err)
	}
	spec, err := s.ProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "stage", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateDeploymentForEnvironmentClone(ctx, a.ID, p.ID, op.ID, op.Revision, app.ID, spec.Hash)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{mainKey, sidecarKey} {
		if _, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
			t.Fatalf("prepared target lost artifact retention: %t, %v", eligible, err)
		}
	}
	if err := s.ClearDeployment(ctx, target.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{mainKey, sidecarKey} {
		claim, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key)
		if err != nil || !eligible || claim.State != state.LayerArtifactDeleting || claim.DeletionID == "" {
			t.Fatalf("unreferenced deletion: %+v, %t, %v", claim, eligible, err)
		}
		retry, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key)
		if err != nil || !eligible || retry.DeletionID != claim.DeletionID {
			t.Fatalf("deletion retry changed identity: %+v, %v", retry, err)
		}
		forged := claim
		forged.DeletionID = uuid.NewString()
		if err := s.CompleteLayerArtifactDeletion(ctx, forged); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("foreign completion accepted: %v", err)
		}
		if err := s.CompleteLayerArtifactDeletion(ctx, claim); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteLayerArtifactDeletion(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err := s.PendingLayerArtifactDeletions(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("completed deletions remain pending: count=%d, err=%v", len(rows), err)
	}
	if err := s.SetDeploymentRootfs(ctx, newer.ID, "/retired.ext4", mainKey, 4096); !errors.Is(err, state.ErrLayerArtifactRetired) {
		t.Fatalf("retired main key was revived: %v", err)
	}
	if _, err := s.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: newer.ID, SidecarName: "retired", StorageKey: sidecarKey, Bytes: 512}); !errors.Is(err, state.ErrLayerArtifactRetired) {
		t.Fatalf("retired sidecar key was revived: %v", err)
	}
}

// ADR-568: either publication owns the key or cleanup owns it, never both.
func TestMemLayerDeletionFencesConcurrentPublication(t *testing.T) {
	layerDeletionFencesConcurrentPublication(t, state.NewMemStore())
}

func layerDeletionFencesConcurrentPublication(t *testing.T, s layerRetentionTestStore) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, "artifact-race@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "artifact-race", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 16 {
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:race"})
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("layers/race-%s-%d", app.ID, i)
		start := make(chan struct{})
		published := make(chan error, 1)
		claimed := make(chan bool, 1)
		claimErrors := make(chan error, 1)
		go func() { <-start; published <- s.SetDeploymentRootfs(ctx, d.ID, "/race.ext4", key, 1024) }()
		go func() {
			<-start
			_, eligible, err := s.ClaimLayerArtifactDeletion(ctx, key)
			claimed <- eligible
			claimErrors <- err
		}()
		close(start)
		publicationErr, eligible, claimErr := <-published, <-claimed, <-claimErrors
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if eligible && !errors.Is(publicationErr, state.ErrLayerArtifactRetired) {
			t.Fatalf("cleanup and publication both acquired key: eligible=%t, err=%v", eligible, publicationErr)
		}
		if !eligible && publicationErr != nil {
			t.Fatalf("retained publication failed: %v", publicationErr)
		}
	}
}
