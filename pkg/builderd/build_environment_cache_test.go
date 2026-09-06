package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestProcessOnePartitionsCacheByBuildEnvironment(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	vm := &fakeVM{environment: BuildEnvironment{BuilderBaseIdentity: "builder-a", TargetPlatform: "linux/amd64"}}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	run := func(slug, artifact string, wantHit bool) {
		t.Helper()
		buildID, deploymentID, _ := seedDeploymentWithSlugContext(t, ctx, store, source, slug)
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
		deployment, err := store.DeploymentByID(ctx, deploymentID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(deployment.RootfsPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != artifact {
			t.Fatalf("artifact=%q want %q", got, artifact)
		}
	}

	run("environment-a-first", "artifact-a", false)
	run("environment-a-hit", "artifact-a", true)
	vm.environment = BuildEnvironment{BuilderBaseIdentity: "builder-b", TargetPlatform: "linux/amd64"}
	run("environment-b-first", "artifact-b", false)
	run("environment-b-hit", "artifact-b", true)
	if vm.spawnCalls != 2 {
		t.Fatalf("VM spawns=%d want 2", vm.spawnCalls)
	}
}

func TestProcessOneDoesNotCacheAcrossBuilderRestage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	output := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(output, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	environmentA := BuildEnvironment{BuilderBaseIdentity: "builder-a", TargetPlatform: "linux/amd64"}
	environmentB := BuildEnvironment{BuilderBaseIdentity: "builder-b", TargetPlatform: "linux/amd64"}
	vm := &fakeVM{environment: environmentA, out: BuildOutcome{OCIImage: output}}
	vm.waitHook = func() { vm.environment = environmentB }
	cache := NewCache(t.TempDir())
	b := New(store, &fakeNotifier{}, vm, cache, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	buildID, _, _ := seedDeploymentWithSlugContext(t, ctx, store, source, "restage")

	if result, err := b.ProcessOne(ctx, buildID); err != nil || result.CacheHit {
		t.Fatalf("ProcessOne result=%+v err=%v", result, err)
	}
	hash, err := hashFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []BuildEnvironment{environmentA, environmentB} {
		recipe := BuildCacheRecipe{
			SourceSHA256: hash, Framework: FrameworkNode, Plan: "pro",
			RuntimeBaseRef:      "ghcr.io/poyrazk/base-minimal:latest",
			BuilderBaseIdentity: environment.BuilderBaseIdentity, TargetPlatform: environment.TargetPlatform,
		}
		if _, ok := cache.LookupBuild(recipe); ok {
			t.Fatalf("build restaged during execution was cached under %+v", environment)
		}
	}
}

func TestProcessOneContinuesWithoutCacheWhenBuildEnvironmentUnavailable(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	output := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(output, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{
		environmentErr: errors.New("builder base is being restaged"),
		out:            BuildOutcome{OCIImage: output},
	}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, slug := range []string{"identity-unavailable-first", "identity-unavailable-second"} {
		buildID, _, _ := seedDeploymentWithSlugContext(t, ctx, store, source, slug)
		result, err := b.ProcessOne(ctx, buildID)
		if err != nil {
			t.Fatal(err)
		}
		if result.CacheHit {
			t.Fatal("build with unavailable environment identity used cache")
		}
	}
	if vm.spawnCalls != 2 {
		t.Fatalf("VM spawns=%d want 2", vm.spawnCalls)
	}
}
