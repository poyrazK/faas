package builderd

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildprogress"
)

// liveStepDrain is how long the follower may keep reading once the build VM
// has finished, so the last steps (the image export) still reach the log.
var liveStepDrain = time.Second

// followBuildSteps writes one "build step:" line to the build's log for each
// new BuildKit step the running builder prints. A deploy used to show only
// "building" until the VM exited, and a ten-minute Maven build looked hung
// (retail-store feedback A3, 2026-11-17). The returned stop waits briefly for
// the console to drain, then ends the follower. Without the capability, or
// when vmmd refuses the stream, the build runs as before with no live steps.
func (b *Builderd) followBuildSteps(ctx context.Context, buildID string, h BuildHandle) (stop func()) {
	follower, ok := b.vm.(BuildConsoleVM)
	if !ok {
		return func() {}
	}
	followCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	tracker := buildprogress.NewStepTracker(api.BuildLiveStepMaxLines, api.BuildTimingStepNameMaxBytes)
	go func() {
		defer close(done)
		err := follower.FollowBuildConsole(followCtx, h, func(line string) {
			if name, ok := tracker.Observe(line); ok {
				b.emitBuildLog(followCtx, buildID, buildprogress.StepLogPrefix+name+"\n")
			}
		})
		if err != nil && followCtx.Err() == nil {
			b.log.Info("builderd: live build steps unavailable", "build", buildID, "instance", h.Instance, "err", err)
		}
	}()
	return func() {
		select {
		case <-done:
		case <-time.After(liveStepDrain):
		}
		cancel()
		<-done
	}
}
