package builderd

// adr: 20261102100000 — a build that misses the cache while an identical
// build is building waits for it and takes the cache hit, so the image is
// built once.

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

func TestIdenticalBuildWaitsForTheBuildInProgress(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	output := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(output, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: output}}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Two services of one account built from the same source, as web and
	// worker on `build: .` are.
	producer, _, _ := seedDeploymentWithSlugContext(t, ctx, store, source, "web")
	waiter, _, _ := seedDeploymentForAccount(t, ctx, store, source, buildAccount(t, store, producer), "worker")

	// The waiter is claimed while the producer's builder VM runs.
	var waiterErr error
	vm.waitHook = func() {
		vm.waitHook = nil
		_, waiterErr = b.ProcessOne(ctx, waiter)
	}
	if _, err := b.ProcessOne(ctx, producer); err != nil {
		t.Fatalf("producer: %v", err)
	}
	if !errors.Is(waiterErr, ErrCoalesced) {
		t.Fatalf("waiter while the producer builds: err = %v, want ErrCoalesced", waiterErr)
	}

	result, err := b.ProcessOne(ctx, waiter)
	if err != nil {
		t.Fatalf("waiter after the producer: %v", err)
	}
	if !result.CacheHit || vm.spawnCalls != 1 {
		t.Fatalf("waiter cache hit = %v, VM spawns = %d; want the producer's cached image and one VM", result.CacheHit, vm.spawnCalls)
	}
}

// A build with nothing identical in progress builds as before.
func TestBuildWithoutAnIdenticalBuildIsNotDeferred(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	output := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(output, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: output}}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	build, _, _ := seedDeploymentWithSlugContext(t, ctx, store, source, "web")
	result, err := b.ProcessOne(ctx, build)
	if err != nil || result.CacheHit || vm.spawnCalls != 1 {
		t.Fatalf("result = %+v, err = %v, spawns = %d; want one fresh build", result, err, vm.spawnCalls)
	}
}
