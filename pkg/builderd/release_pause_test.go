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

// TestReleasePauseHoldsQueuedBuilds pins ADR 20261022140000: while the
// break-glass release pause is on, neither claim path takes a queued build,
// and the build runs once the pause is lifted.
func TestReleasePauseHoldsQueuedBuilds(t *testing.T) {
	store := state.NewMemStore()
	srcTar := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, srcTar, []string{"package.json", "index.js"})
	buildID, _, _ := seedDeployment(t, store, srcTar)
	out := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(out, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: out, ExitCode: 0, LogTailBytes: 14}}
	paused := true
	b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithReleasePause(func() bool { return paused })
	ctx := context.Background()

	if _, err := b.ProcessOne(ctx, buildID); err != nil {
		t.Fatalf("ProcessOne while paused: %v", err)
	}
	if _, err := b.ProcessNext(ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ProcessNext while paused = %v, want ErrNotFound (an empty queue to the poller)", err)
	}
	if build, err := store.BuildByID(ctx, buildID); err != nil || build.Status != state.BuildQueued {
		t.Fatalf("build while paused = %s, %v; want it still queued", build.Status, err)
	}
	if fvm.spawnCalls != 0 {
		t.Fatalf("spawned %d builder VMs while paused", fvm.spawnCalls)
	}

	paused = false
	if _, err := b.ProcessNext(ctx); err != nil {
		t.Fatalf("ProcessNext after the pause: %v", err)
	}
	if build, err := store.BuildByID(ctx, buildID); err != nil || build.Status != state.BuildSucceeded {
		t.Fatalf("build after the pause = %s, %v; want succeeded", build.Status, err)
	}
}
