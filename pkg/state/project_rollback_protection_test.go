package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectRollbackProtectionFencesArtifactGC(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "protect-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "protected-shop"})
	if err != nil {
		t.Fatal(err)
	}
	group := uuid.NewString()
	var keys []string
	var sidecarKey string
	for _, name := range []string{"web", "api"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "protected-" + name,
			WorkloadName: name, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		target := createProjectProtectionDeployment(t, store, app.ID, group)
		keys = append(keys, target.RootfsKey)
		if name == "web" {
			sidecarKey = target.RootfsKey + "-sidecar"
			if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
				DeploymentID: target.ID, SidecarName: "side", StorageKey: sidecarKey, Bytes: 2048, ContentDigest: "sha256:" + uuid.NewString(),
			}); err != nil {
				t.Fatal(err)
			}
		}
		for range api.SnapshotRollbackRetentionDeployments {
			createProjectProtectionDeployment(t, store, app.ID, "")
		}
	}
	keys = append(keys, sidecarKey, "sigs/"+sidecarKey+".sig")
	artifacts, err := store.ProjectRollbackProtectionArtifacts(ctx, account.ID, project.ID, group)
	if err != nil || len(artifacts) != 3 {
		t.Fatalf("artifact inventory: %+v %v", artifacts, err)
	}
	hold, err := store.ProtectProjectRollbackDeploy(ctx, account.ID, project.ID, group, 3600, artifacts)
	if err != nil || hold.LayerBytes != 10240 || !hold.ExpiresAt.After(time.Now()) {
		t.Fatalf("protected deploy: %+v %v", hold, err)
	}
	targets, err := store.ListProjectRollbackTargets(ctx, account.ID, project.ID)
	if err != nil || len(targets) != 1 || targets[0].Protection == nil || !targets[0].Protection.ExpiresAt.Equal(hold.ExpiresAt) {
		t.Fatalf("protection missing from targets: %+v %v", targets, err)
	}
	for _, key := range keys {
		if _, eligible, err := store.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
			t.Fatalf("GC claimed protected artifact %q: %v %v", key, eligible, err)
		}
	}
	tag, err := pool.Exec(ctx, `update layer_artifact_retention set state='deleting', deletion_id=$2 where storage_key=$1`, keys[0], uuid.NewString())
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("legacy GC escaped the fence: %v %v", tag, err)
	}
	if _, err := pool.Exec(ctx, `update project_rollback_deploy_protections set expires_at=clock_timestamp()-interval '1 second' where project_id=$1`, project.ID); err != nil {
		t.Fatal(err)
	}
	targets, err = store.ListProjectRollbackTargets(ctx, account.ID, project.ID)
	if err != nil || len(targets) != 1 || targets[0].Protection != nil {
		t.Fatalf("expired protection is still listed: %+v %v", targets, err)
	}
	for _, key := range keys {
		if _, eligible, err := store.ClaimLayerArtifactDeletion(ctx, key); err != nil || !eligible {
			t.Fatalf("GC could not claim expired artifact %q: %v %v", key, eligible, err)
		}
	}
	if _, err := store.ProtectProjectRollbackDeploy(ctx, account.ID, project.ID, group, 3600, artifacts); err == nil {
		t.Fatal("protection was accepted after GC claimed the artifacts")
	}
}

func createProjectProtectionDeployment(t *testing.T, store state.Store, app, group string) state.Deployment {
	t.Helper()
	ctx := t.Context()
	id := uuid.NewString()
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + id, ProjectDeployGroupID: group, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	key := "layers/" + id
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/"+key, key, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	deployment.RootfsKey = key
	return deployment
}
