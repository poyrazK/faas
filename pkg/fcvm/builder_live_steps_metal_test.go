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
// and records what arrived and when (retail-store feedback A3: steps must
// show while the build runs, not after the VM exits).
type liveStepRecorder struct {
	mu        sync.Mutex
	lines     int
	firstLine time.Time
	sample    []string
	steps     []string
	firstStep time.Time
	stop      context.CancelFunc
	done      chan error
	stopped   bool
	followErr error
}

func followLiveSteps(ctx context.Context, driver *builderd.VMMDriver, handle builderd.BuildHandle) *liveStepRecorder {
	followCtx, stop := context.WithCancel(ctx)
	r := &liveStepRecorder{stop: stop, done: make(chan error, 1)}
	tracker := buildprogress.NewStepTracker(api.BuildLiveStepMaxLines, api.BuildTimingStepNameMaxBytes)
	go func() {
		r.done <- driver.FollowBuildConsole(followCtx, handle, func(line string) {
			now := time.Now()
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.lines == 0 {
				r.firstLine = now
			}
			r.lines++
			if len(r.sample) < 5 {
				r.sample = append(r.sample, line)
			}
			if name, ok := tracker.Observe(line); ok {
				if r.firstStep.IsZero() {
					r.firstStep = now
				}
				r.steps = append(r.steps, name)
			}
		})
	}()
	return r
}

// finish lets the console drain briefly, then ends the follower.
func (r *liveStepRecorder) finish() {
	if r.stopped {
		return
	}
	r.stopped = true
	select {
	case r.followErr = <-r.done:
	case <-time.After(2 * time.Second):
		r.stop()
		r.followErr = <-r.done
	}
	r.stop()
}

// assertLive requires console lines that arrived before the VM finished:
// vmmd's ring carries the builder's console and the driver streams it live.
// When the guest reached BuildKit (buildRan), it also requires a step that
// arrived before the build finished.
func (r *liveStepRecorder) assertLive(t *testing.T, finished time.Time, buildRan bool) {
	t.Helper()
	r.finish()
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Logf("live console: %d lines (first %q), steps %q, follow err %v", r.lines, r.sample, r.steps, r.followErr)
	if r.followErr != nil {
		t.Fatalf("follow builder console: %v", r.followErr)
	}
	if r.lines == 0 || !r.firstLine.Before(finished) {
		t.Fatalf("no builder console line streamed before the VM finished at %s", finished)
	}
	if !buildRan {
		return
	}
	if len(r.steps) == 0 {
		t.Fatal("no BuildKit step reached the live console stream")
	}
	if !r.firstStep.Before(finished) {
		t.Fatalf("first step arrived at %s, after the build finished at %s", r.firstStep, finished)
	}
}
