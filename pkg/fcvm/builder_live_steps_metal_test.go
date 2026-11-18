//go:build metal && linux

package fcvm_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/buildprogress"
)

// liveStepRecorder follows a running builder's console the way builderd does
// and records each BuildKit step with when it arrived (retail-store feedback
// A3: steps must show while the build runs, not after the VM exits).
type liveStepRecorder struct {
	mu    sync.Mutex
	steps []string
	first time.Time
	stop  context.CancelFunc
	done  chan error
}

func followLiveSteps(ctx context.Context, driver *builderd.VMMDriver, handle builderd.BuildHandle) *liveStepRecorder {
	followCtx, stop := context.WithCancel(ctx)
	r := &liveStepRecorder{stop: stop, done: make(chan error, 1)}
	tracker := buildprogress.NewStepTracker(api.BuildLiveStepMaxLines, api.BuildTimingStepNameMaxBytes)
	go func() {
		r.done <- driver.FollowBuildConsole(followCtx, handle, func(line string) {
			if name, ok := tracker.Observe(line); ok {
				r.mu.Lock()
				if r.first.IsZero() {
					r.first = time.Now()
				}
				r.steps = append(r.steps, name)
				r.mu.Unlock()
			}
		})
	}()
	return r
}

// assertLive stops the follower and requires at least one step that arrived
// before the build finished.
func (r *liveStepRecorder) assertLive(t *testing.T, finished time.Time) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		r.stop()
		<-r.done
	}
	r.stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Logf("live build steps (%d): %q", len(r.steps), r.steps)
	if len(r.steps) == 0 {
		t.Fatal("no BuildKit step reached the live console stream")
	}
	if !r.first.Before(finished) {
		t.Fatalf("first step arrived at %s, after the build finished at %s", r.first, finished)
	}
}
