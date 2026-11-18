package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildprogress"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectBuildStepInterval is how often a project wait reads a building
// service's newest build step. builderd writes one "build step:" log line per
// BuildKit step while the builder runs; the wait used to show only
// "image_build" for the whole build (retail-store feedback A3, 2026-11-17).
var projectBuildStepInterval = 10 * time.Second

// projectBuildStepLogRows is how many of the newest log rows one read scans.
// Step lines are a small share of a build log, but a RUN step's own output
// is not in it: the build log carries steps and builderd's own notes.
const projectBuildStepLogRows = 40

// projectBuildStepPoller reads one deployment's newest build step while it
// builds, at most once per projectBuildStepInterval.
type projectBuildStepPoller struct {
	last time.Time
}

func (s *projectBuildStepPoller) poll(ctx context.Context, client rolloutStatusClient, progress *projectWaitProgress, build api.AppliedBuild, dep api.DeploymentResponse, phase string) {
	if progress == nil {
		return
	}
	logs, ok := client.(deploymentLogStreamer)
	if !ok {
		return
	}
	stage := projectDeploymentStage(dep, phase, false, "")
	if stage != string(state.StageImageBuild) && stage != string(state.StageDependencyRestore) {
		return
	}
	if !s.last.IsZero() && time.Since(s.last) < projectBuildStepInterval {
		return
	}
	s.last = time.Now()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if step := buildprogress.LatestStep(readRecentDeploymentLog(readCtx, logs, build.DeploymentID, projectBuildStepLogRows)); step != "" {
		progress.observeStep(build, step)
	}
}

// projectBuildStepEvent is the JSON-mode line for a new build step.
type projectBuildStepEvent struct {
	Event        string `json:"event"`
	Service      string `json:"service"`
	DeploymentID string `json:"deployment_id"`
	Step         string `json:"step"`
	ElapsedS     int64  `json:"elapsed_s"`
}

// observeStep reports a building service's newest step once.
func (p *projectWaitProgress) observeStep(build api.AppliedBuild, step string) {
	if p == nil || step == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	svc, seen := p.services[build.DeploymentID]
	if !seen || svc.done || svc.step == step {
		return
	}
	svc.step = step
	p.services[build.DeploymentID] = svc
	elapsed := time.Since(p.start).Truncate(time.Second)
	if p.json {
		line, _ := json.Marshal(projectBuildStepEvent{
			Event: "service_build_step", Service: svc.service, DeploymentID: build.DeploymentID,
			Step: step, ElapsedS: int64(elapsed / time.Second),
		})
		_, _ = fmt.Fprintf(p.w, "%s\n", line)
		return
	}
	_, _ = fmt.Fprintf(p.w, "  %s: %s %s (%s)\n", svc.service, svc.stage, terminalSafe(step), elapsed)
}
