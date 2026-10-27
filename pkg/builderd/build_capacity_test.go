package builderd

// adr: 20261028090000 — a builder without disk for a build drive returns the
// build to the queue; it does not fail the deployment.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestProcessOne_BuildDiskFullRequeuesTheBuild(t *testing.T) {
	previous := buildDiskCapacityRetryDelay
	buildDiskCapacityRetryDelay = time.Millisecond
	t.Cleanup(func() { buildDiskCapacityRetryDelay = previous })

	ctx := context.Background()
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})
	buildID, depID, _ := seedDeployment(t, store, src)
	fvm := &fakeVM{spawnErr: fmt.Errorf("create drive1: %w: available=1 bytes, required=2 bytes", ErrBuildDiskFull)}
	b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := b.ProcessOne(ctx, buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	build, _ := store.BuildByID(ctx, buildID)
	dep, _ := store.DeploymentByID(ctx, depID)
	if build.Status != state.BuildQueued || dep.Status == state.DeployFailed || fvm.spawnCalls != 1 {
		t.Fatalf("build=%s deployment=%s spawns=%d; want the build requeued and the deployment in flight", build.Status, dep.Status, fvm.spawnCalls)
	}
}

func TestReclaimDependencyCachesFreesOldestFirst(t *testing.T) {
	driveDir := t.TempDir()
	root := filepath.Join(driveDir, devDependencyCacheDir)
	now := time.Now()
	write := func(key string, age time.Duration) string {
		t.Helper()
		dir := filepath.Join(root, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "layer"), make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
		at := now.Add(-age)
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	oldest := write(strings.Repeat("a", 64), 3*time.Hour)
	newest := write(strings.Repeat("b", 64), time.Hour)

	reclaimDependencyCaches(driveDir, 1024, now)

	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("oldest cache entry survived the reclaim: %v", err)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("newest cache entry was reclaimed beyond the need: %v", err)
	}
}
