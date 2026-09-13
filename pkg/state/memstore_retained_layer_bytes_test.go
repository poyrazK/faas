package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreRetainedLayerBytesDeduplicatesAndDropsClearedDeployments(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "retained-layers@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "retained-layers"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: "image", ImageDigest: "sha256:first", Status: DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: "image", ImageDigest: "sha256:second", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, first.ID, "/layers/shared", "layers/shared", 10); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, second.ID, "/layers/shared", "layers/shared", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentSidecarLayer(ctx, DeploymentSidecarLayer{
		DeploymentID: second.ID, SidecarName: "proxy", StorageKey: "layers/proxy", Bytes: 5, ContentDigest: "sha256:proxy",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.RetainedLayerBytes(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != 15 {
		t.Fatalf("RetainedLayerBytes = %d, want 15 (shared rootfs once + sidecar)", got)
	}
	if err := store.ClearDeployment(ctx, first.ID, "test"); err != nil {
		t.Fatal(err)
	}
	got, err = store.RetainedLayerBytes(ctx, app.ID)
	if err != nil || got != 15 {
		t.Fatalf("after clear = %d, %v; want 15 from live deployment", got, err)
	}
}
