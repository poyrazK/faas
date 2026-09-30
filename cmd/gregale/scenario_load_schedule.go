package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"
)

type testLoadStageEvidence struct {
	Index             int   `json:"index"`
	StartVUs          int   `json:"start_vus"`
	TargetVUs         int   `json:"target_vus"`
	DurationMS        int64 `json:"duration_ms"`
	IterationsStarted int   `json:"iterations_started"`
}

type testLoadProgress struct {
	Elapsed              time.Duration
	Stage, StageCount    int
	TargetVUs, ActiveVUs int
	Started, Completed   int
	Requests, Failures   int
	Draining             bool
	ArrivalRate          int
	Scheduled, Dropped   int
}

func (cfg *testLoadConfig) maxVUs() int {
	maximum := cfg.VUs
	for _, stage := range cfg.Stages {
		if stage.Target > maximum {
			maximum = stage.Target
		}
	}
	return maximum
}

// Targets interpolate linearly and round to the nearest whole user. The pool
// keeps stable user slots; a ramp down stops those slots at journey boundaries.
func (cfg *testLoadConfig) targetAt(elapsed time.Duration) (int, int) {
	if cfg.Duration > 0 && elapsed >= cfg.Duration {
		return 0, len(cfg.Stages)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	previous := cfg.VUs
	for i, stage := range cfg.Stages {
		if elapsed < stage.Duration {
			fraction := float64(elapsed) / float64(stage.Duration)
			return int(math.Round(float64(previous) + float64(stage.Target-previous)*fraction)), i
		}
		elapsed -= stage.Duration
		previous = stage.Target
	}
	return cfg.VUs, -1
}

func waitTestLoad(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *testLoadCollector) progress(cfg *testLoadConfig, elapsed time.Duration) testLoadProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	target, stage := cfg.targetAt(elapsed)
	progress := testLoadProgress{Elapsed: elapsed, Stage: stage + 1, StageCount: len(cfg.Stages), TargetVUs: target, ActiveVUs: c.active, Started: c.started, Completed: c.completed, Draining: cfg.Duration > 0 && elapsed >= cfg.Duration}
	if cfg.Rate > 0 {
		progress.ArrivalRate, progress.TargetVUs = cfg.Rate, cfg.maxVUs()
		progress.Scheduled, progress.Dropped = c.arrivalsScheduled, c.droppedCapacity+c.droppedLate
	}
	for _, step := range c.steps {
		progress.Requests += step.evidence.Requests
		progress.Failures += step.evidence.Failures
	}
	return progress
}

// Progress owns its writer until stop returns, before assertions or cleanup run.
func monitorTestLoadProgress(ctx context.Context, c *testLoadCollector, cfg *testLoadConfig, started time.Time) func() {
	if cfg.Progress == nil {
		return func() {}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				cfg.Progress(c.progress(cfg, time.Since(started)))
			}
		}
	}()
	return func() { close(stop); <-done }
}

func printTestLoadProgress(out io.Writer, progress testLoadProgress) {
	phase := ""
	if progress.Draining {
		phase = ", draining"
	} else if progress.StageCount > 0 {
		phase = fmt.Sprintf(", stage %d/%d", progress.Stage, progress.StageCount)
	}
	if progress.ArrivalRate > 0 {
		_, _ = fmt.Fprintf(out, "  load %.1fs%s: target %d journeys/s, %d/%d VUs active; %d/%d arrivals started, %d dropped; %d/%d journeys completed, %d HTTP steps, %d failures\n", progress.Elapsed.Seconds(), phase, progress.ArrivalRate, progress.ActiveVUs, progress.TargetVUs, progress.Started, progress.Scheduled, progress.Dropped, progress.Completed, progress.Started, progress.Requests, progress.Failures)
		return
	}
	_, _ = fmt.Fprintf(out, "  load %.1fs%s: target %d VUs, %d active; %d/%d journeys completed, %d HTTP steps, %d failures\n", progress.Elapsed.Seconds(), phase, progress.TargetVUs, progress.ActiveVUs, progress.Completed, progress.Started, progress.Requests, progress.Failures)
}
