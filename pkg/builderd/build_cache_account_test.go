package builderd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A builder VM's output is tenant-controlled, so two accounts deploying the
// same source never share a cache entry; one account's apps still do.
func TestBuildCacheIsPartitionedByAccount(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	vm := &fakeVM{}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	build := func(buildID, deploymentID, artifact string, wantHit bool) {
		t.Helper()
		output := filepath.Join(t.TempDir(), "image.tar")
		if err := os.WriteFile(output, []byte(artifact), 0o600); err != nil {
			t.Fatal(err)
		}
		vm.out = BuildOutcome{OCIImage: output}
		result, err := b.ProcessOne(ctx, buildID)
		if err != nil {
			t.Fatal(err)
		}
		if result.CacheHit != wantHit {
			t.Fatalf("cache hit=%v want %v", result.CacheHit, wantHit)
		}
		dep, err := store.DeploymentByID(ctx, deploymentID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dep.RootfsPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != artifact {
			t.Fatalf("artifact = %q, want %q", got, artifact)
		}
	}

	attackerBuild, attackerDep, _ := seedDeploymentWithSlugContext(t, ctx, store, source, "attacker")
	build(attackerBuild, attackerDep, "poisoned", false)
	victimBuild, victimDep, _ := seedDeploymentWithSlugContext(t, ctx, store, source, "victim")
	build(victimBuild, victimDep, "victim's own build", false)
	victim := buildAccount(t, store, victimBuild)
	sameAccountBuild, sameAccountDep, _ := seedDeploymentForAccount(t, ctx, store, source, victim, "victim-second-app")
	build(sameAccountBuild, sameAccountDep, "victim's own build", true)
	if vm.spawnCalls != 2 {
		t.Fatalf("VM spawns = %d, want 2", vm.spawnCalls)
	}

	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "")
	a, err := withAccount(recipe, "acct-a").key()
	if err != nil {
		t.Fatal(err)
	}
	other, err := withAccount(recipe, "acct-b").key()
	if err != nil || a == other {
		t.Fatalf("two accounts share cache key %s (%v)", a, err)
	}
}
