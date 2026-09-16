package builderd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func exportGCFixture(t *testing.T, root string) (*state.MemStore, state.Build, state.Deployment, string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "export-gc@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "export-gc", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, build.ID, "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("oci"), 0o644); err != nil {
		t.Fatal(err)
	}
	return store, build, dep, artifact
}

func TestSweepBuildExportsPreservesThenReleasesSuccessfulHandoff(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, build, dep, artifact := exportGCFixture(t, root)
	claim, err := store.ClaimQueuedBuild(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteBuild(ctx, claim, artifact, "apps/export-gc/layer.ext4", 3, state.BuildProvenance{BuildID: build.ID}); err != nil {
		t.Fatal(err)
	}
	cfg := BuildExportGCConfig{Root: root, MaxBytes: 1 << 20, MaxAge: 24 * time.Hour, OrphanMinAge: time.Hour}
	result, err := SweepBuildExports(ctx, store, cfg, time.Now())
	if err != nil || result.Removed != 0 || result.SkippedActive != 1 {
		t.Fatalf("pending handoff sweep = (%+v, %v), want protected", result, err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, filepath.Join(root, "published.ext4"), "apps/export-gc/layer.ext4", 3); err != nil {
		t.Fatal(err)
	}
	result, err = SweepBuildExports(ctx, store, cfg, time.Now())
	if err != nil || result.Removed != 1 || result.RemovedByReason["released"] != 1 {
		t.Fatalf("published handoff sweep = (%+v, %v), want released", result, err)
	}
}

func TestSweepBuildExportsRemovesFailedAndCancelled(t *testing.T) {
	for _, status := range []state.BuildStatus{state.BuildFailed, state.BuildCancelled} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			store, build, _, artifact := exportGCFixture(t, root)
			if _, err := store.ClaimQueuedBuild(ctx, build.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateBuildStatus(ctx, build.ID, status, "terminal", false, true); err != nil {
				t.Fatal(err)
			}
			result, err := SweepBuildExports(ctx, store, BuildExportGCConfig{
				Root: root, MaxBytes: 1 << 20, MaxAge: 24 * time.Hour, OrphanMinAge: time.Hour,
			}, time.Now())
			if err != nil || result.Removed != 1 {
				t.Fatalf("terminal sweep = (%+v, %v), want removal", result, err)
			}
			if _, err := os.Stat(artifact); !os.IsNotExist(err) {
				t.Fatalf("terminal artifact remains: %v", err)
			}
		})
	}
}
