package builderd

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A builder whose disk stayed short requeued every build forever, holding
// source deploys in "building" until the six-hour deployment watchdog (bug
// hunt H83). Past BuildDiskCapacityMaxWait since it was enqueued a build is
// no longer requeued, so it fails with the disk-full error.
func TestBuildDiskShortfallStopsRequeuingPastTheWaitBound(t *testing.T) {
	previous := buildDiskCapacityRetryDelay
	buildDiskCapacityRetryDelay = time.Millisecond
	t.Cleanup(func() { buildDiskCapacityRetryDelay = previous })

	b := New(state.NewMemStore(), &fakeNotifier{}, &fakeVM{}, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	stale := state.Build{ID: "build-stale", DeploymentID: "dep", EnqueuedAt: time.Now().Add(-api.BuildDiskCapacityMaxWait - time.Minute)}
	if b.requeueForBuildCapacity(context.Background(), stale, ErrBuildDiskFull) {
		t.Fatal("a build past the wait bound was requeued again")
	}
}
