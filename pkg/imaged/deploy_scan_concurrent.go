package imaged

import (
	"context"
	"sync"
)

// deployGrype is a per-deploy Grype scan running beside the artifact hash
// (deploy-path review P15). Its phases (extract, view, grype) are timed on
// their own timeline because they overlap the caller's.
type deployGrype struct {
	cancel context.CancelFunc
	done   chan struct{}
	timing *phaseTimer
	once   sync.Once

	result  *ScanResult
	err     error
	stopped bool
}

// startDeployGrype starts Grype on dir. The caller must stop or wait for it
// before dir is removed.
func (h *Handler) startDeployGrype(ctx context.Context, dir string) *deployGrype {
	scanCtx, cancel := context.WithCancel(ctx)
	scanCtx, timing := withScanTiming(scanCtx)
	g := &deployGrype{cancel: cancel, done: make(chan struct{}), timing: timing}
	go func() {
		defer close(g.done)
		g.result, g.err = h.runGrype(scanCtx, dir)
	}()
	return g
}

// wait returns the scan's result.
func (g *deployGrype) wait() (*ScanResult, error) {
	<-g.done
	g.once.Do(g.cancel)
	return g.result, g.err
}

// stop cancels the scan, if it is still running, and waits for it to exit.
// Its result is then unused, so its phases are not reported.
func (g *deployGrype) stop() {
	select {
	case <-g.done:
	default:
		g.stopped = true
	}
	g.once.Do(g.cancel)
	<-g.done
}

// phaseAttrs returns the scan's phases unless it was stopped early.
func (g *deployGrype) phaseAttrs() []any {
	if g.stopped {
		return nil
	}
	return g.timing.phaseAttrs()
}
