package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A launch has one active line, driven by deployment stage events. The clock
// only updates elapsed time; it never advances a stage or estimates completion.
type startProgress struct {
	mu        sync.Mutex
	writer    io.Writer
	live      bool
	startedAt time.Time
	label     string
	stage     int
	failed    bool
	closed    bool
	stop      chan struct{}
	done      chan struct{}
}

func newStartProgress(w io.Writer, live bool, label string) *startProgress {
	p := &startProgress{writer: w, live: live, startedAt: time.Now(), label: label, stage: -1, stop: make(chan struct{}), done: make(chan struct{})}
	p.render()
	if live {
		go p.runClock()
	} else {
		close(p.done)
	}
	return p
}

func (p *startProgress) runClock() {
	defer close(p.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			if !p.closed && !p.failed {
				p.render()
			}
			p.mu.Unlock()
		}
	}
}

func (p *startProgress) observeStage(name, status string, _ int64, _ string) {
	if status != stageStatusInProgress && status != stageStatusCompleted && status != stageStatusFailed {
		return
	}
	index := -1
	for i, stage := range stageOrder {
		if string(stage) == name {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	label := "Building app"
	switch index {
	case 4:
		label = "Starting app"
	case 5:
		label = "Checking readiness"
	}
	failed := status == stageStatusFailed
	if failed {
		label = "Build failed"
		switch index {
		case 4:
			label = "App startup failed"
		case 5:
			label = "Readiness check failed"
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Replayed events cannot rewind the display or undo a reported failure.
	if p.closed || p.failed || index < p.stage {
		return
	}
	p.stage = index
	if label == p.label {
		return
	}
	if p.live {
		_, _ = fmt.Fprintln(p.writer)
	}
	p.label, p.failed = label, failed
	p.render()
}

func (p *startProgress) observeDeployment(dep api.DeploymentResponse) {
	var stages struct {
		History []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"history"`
	}
	if json.Unmarshal(dep.StageState, &stages) != nil {
		return
	}
	for _, stage := range stages.History {
		p.observeStage(stage.Name, stage.Status, 0, "")
	}
}

// render is called under mu once the clock has started.
func (p *startProgress) render() {
	prefix := ""
	if p.live {
		prefix = "\r" + escapeClearLine + GlyphProgress + " "
		if p.failed {
			prefix = "\r" + escapeClearLine + GlyphFail + " "
		}
	}
	_, _ = fmt.Fprintf(p.writer, "%s%s · %s elapsed", prefix, p.label, p.elapsed())
	if !p.live {
		_, _ = fmt.Fprintln(p.writer)
	}
}

func (p *startProgress) elapsed() time.Duration {
	return time.Since(p.startedAt).Truncate(time.Second)
}

// Close joins the clock before any prompt, error, or success screen is printed.
func (p *startProgress) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.stop)
		if p.live {
			_, _ = fmt.Fprintln(p.writer)
		}
	}
	p.mu.Unlock()
	<-p.done
}
