package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectWaitProgress reports what a waiting project deploy is doing. The
// wait used to stay silent from upload until every service finished, while
// the server built, scanned, snapshotted and gated dependencies for minutes;
// a quiet terminal looked like a hang and a failed upstream showed only in
// the final receipt (retail-store GREG-04).
//
// Human mode writes one line per service stage change plus a periodic
// "still waiting" summary. JSON mode writes one service_stage_changed
// object per change. Both go to stderr, so stdout keeps only the receipt.
type projectWaitProgress struct {
	w        io.Writer
	json     bool
	start    time.Time
	mu       sync.Mutex
	services map[string]projectServiceProgress // by deployment ID
}

type projectServiceProgress struct {
	service string
	stage   string
	done    bool
}

// projectWaitProgressEvent is the JSON-mode progress line.
type projectWaitProgressEvent struct {
	Event        string `json:"event"`
	Service      string `json:"service"`
	DeploymentID string `json:"deployment_id"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	ElapsedS     int64  `json:"elapsed_s"`
}

// projectWaitHeartbeat is how often human mode summarises a wait with no
// stage change.
var projectWaitHeartbeat = 30 * time.Second

type projectWaitProgressKey struct{}

func withProjectWaitProgress(ctx context.Context, w io.Writer, jsonMode bool) context.Context {
	return context.WithValue(ctx, projectWaitProgressKey{}, &projectWaitProgress{
		w: w, json: jsonMode, start: time.Now(), services: map[string]projectServiceProgress{},
	})
}

func projectWaitProgressFrom(ctx context.Context) *projectWaitProgress {
	p, _ := ctx.Value(projectWaitProgressKey{}).(*projectWaitProgress)
	return p
}

// projectDeploymentStage names what a deployment is doing now: the server's
// current pipeline stage while it builds, otherwise the wait phase.
func projectDeploymentStage(dep api.DeploymentResponse, phase string, done bool, failure string) string {
	switch {
	case done && (failure != "" || isTerminalDeploymentStatus(dep.Status) && dep.Status != statusLive):
		return "failed"
	case done:
		return "live"
	}
	var ss state.StageState
	if len(dep.StageState) > 0 && json.Unmarshal(dep.StageState, &ss) == nil && ss.Current != "" {
		return string(ss.Current)
	}
	if phase == "" || phase == "deployment" {
		if dep.Status != "" {
			return dep.Status
		}
		return "queued"
	}
	return phase
}

func (p *projectWaitProgress) observe(build api.AppliedBuild, dep api.DeploymentResponse, phase string, done bool, failure string) {
	if p == nil {
		return
	}
	service := build.Slug
	if service == "" {
		service = build.DeploymentID
	}
	stage := projectDeploymentStage(dep, phase, done, failure)
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, seen := p.services[build.DeploymentID]
	if seen && prev.stage == stage {
		return
	}
	p.services[build.DeploymentID] = projectServiceProgress{service: service, stage: stage, done: done}
	elapsed := time.Since(p.start).Truncate(time.Second)
	errText := failure
	if errText == "" && stage == "failed" {
		errText = dep.Error
	}
	if p.json {
		line, _ := json.Marshal(projectWaitProgressEvent{
			Event: "service_stage_changed", Service: service, DeploymentID: build.DeploymentID,
			Stage: stage, Status: dep.Status, Error: errText, ElapsedS: int64(elapsed / time.Second),
		})
		_, _ = fmt.Fprintf(p.w, "%s\n", line)
		return
	}
	switch stage {
	case "failed":
		_, _ = fmt.Fprintf(p.w, "  %s: failed after %s: %s\n", service, elapsed, oneLine(errText))
	case "live":
		_, _ = fmt.Fprintf(p.w, "  %s: live after %s\n", service, elapsed)
	default:
		_, _ = fmt.Fprintf(p.w, "  %s: %s (%s)\n", service, stage, elapsed)
	}
}

// heartbeat prints which services are still running, in human mode only.
func (p *projectWaitProgress) heartbeat() {
	if p == nil || p.json {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var waiting []string
	for _, s := range p.services {
		if !s.done {
			waiting = append(waiting, s.service+" ("+s.stage+")")
		}
	}
	if len(waiting) == 0 {
		return
	}
	sort.Strings(waiting)
	_, _ = fmt.Fprintf(p.w, "  still waiting after %s on %s\n", time.Since(p.start).Truncate(time.Second), strings.Join(waiting, ", "))
}

// runHeartbeat calls heartbeat every projectWaitHeartbeat until stop closes.
func (p *projectWaitProgress) runHeartbeat(stop <-chan struct{}) {
	if p == nil || p.json || projectWaitHeartbeat <= 0 {
		return
	}
	ticker := time.NewTicker(projectWaitHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.heartbeat()
		}
	}
}
