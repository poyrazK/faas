//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildprogress"
)

// buildClock records where a build spends its time in the guest, for
// BuildDone.Timings: guest-init's milestones, the build command's wall time
// and BuildKit's slowest steps. builderd writes them into the build log, so
// a slow build can be read without the node's console log.
type buildClock struct {
	start        time.Time
	guestStartMS int64
	stages       []api.BuildStageTiming
	buildMS      int64
	progress     buildprogress.Summary
}

func newBuildClock() *buildClock {
	return &buildClock{start: time.Now(), guestStartMS: uptimeMS()}
}

// stage logs a milestone, as guestStage does, and records when it happened.
func (c *buildClock) stage(name string) {
	guestStage(name)
	c.stages = append(c.stages, api.BuildStageTiming{Stage: name, AtMS: time.Since(c.start).Milliseconds()})
}

// finishBuild records the build command's wall time and its BuildKit
// progress output.
func (c *buildClock) finishBuild(elapsed time.Duration, output []byte) {
	c.buildMS = elapsed.Milliseconds()
	c.progress = buildprogress.Summarize(output, api.BuildTimingMaxSteps, api.BuildTimingStepNameMaxBytes)
}

func (c *buildClock) timings() *api.BuildTimings {
	t := &api.BuildTimings{
		GuestStartMS:  c.guestStartMS,
		Stages:        c.stages,
		BuildMS:       c.buildMS,
		ImageExportMS: c.progress.ImageExport.Milliseconds(),
		CacheExportMS: c.progress.CacheExport.Milliseconds(),
	}
	for _, step := range c.progress.Slowest {
		t.SlowestSteps = append(t.SlowestSteps, api.BuildStepTiming{Name: step.Name, MS: step.Duration.Milliseconds()})
	}
	return t
}

// uptimeMS is the time since this guest's kernel booted, from /proc/uptime;
// zero when unreadable.
func uptimeMS() int64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return int64(seconds * 1000)
}
