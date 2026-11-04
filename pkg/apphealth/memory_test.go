// adr: 20261102124306
package apphealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// App health warns when the app's guests ran short of memory within the
// rollup window, and only while the check is released (ADR-20261102124306).
func TestMemoryHealth(t *testing.T) {
	recent := func(e *Evidence) {
		e.Memory = MemoryEvidence{Known: true, Recorded: true, Pressure: state.AppMemoryPressure{
			WindowStartedAt: e.Now.Add(-5 * time.Hour), LastAt: e.Now.Add(-time.Hour),
			Episodes: 3, LowestAvailableMB: 23, TotalMB: 256, PeakAnonMB: 221}}
	}
	for _, tt := range []struct {
		name   string
		change func(*Evidence)
		status string
		state  string
	}{
		{"not released", func(e *Evidence) { e.AssessMemory = false; recent(e) }, Healthy, ""},
		{"never reported", func(e *Evidence) { e.Memory = MemoryEvidence{Known: true} }, Healthy, Pass},
		{"recent pressure", recent, Degraded, Warning},
		{"old pressure", func(e *Evidence) {
			recent(e)
			e.Memory.Pressure.LastAt = e.Now.Add(-api.AppHealthMemoryPressureWindow - time.Minute)
		}, Healthy, Pass},
		{"read failed", func(e *Evidence) { e.Memory = MemoryEvidence{} }, Unknown, Unknown},
		{"worker pool", func(e *Evidence) { workerOf(e); recent(e) }, Degraded, Warning},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := healthEvidence()
			e.AssessMemory = true
			tt.change(&e)
			out := Evaluate(e)
			if out.Status != tt.status || checkStatus(out, "memory") != tt.state {
				t.Fatalf("got %s memory=%q, want %s memory=%q: %+v", out.Status, checkStatus(out, "memory"), tt.status, tt.state, out.Checks)
			}
		})
	}
	e := healthEvidence()
	e.AssessMemory = true
	recent(&e)
	if got := Evaluate(e).Summary; !strings.Contains(got, "fell to 23 of 256 MB (8%) 3 times in the last 5 hours; anonymous memory peaked at 221 MB") {
		t.Fatalf("summary = %q", got)
	}
}

func workerOf(e *Evidence) {
	e.App.Manifest.ExecutionMode = api.ExecutionModeWorker
	e.AssessWorkers = true
	e.Worker = WorkerQueue{Known: true, Triggers: 1}
}

type memoryReaderFake struct {
	workerQueueFake
	p        state.AppMemoryPressure
	recorded bool
	err      error
}

func (f *memoryReaderFake) AppMemoryPressure(context.Context, string) (state.AppMemoryPressure, bool, error) {
	return f.p, f.recorded, f.err
}

func TestCollectMemory(t *testing.T) {
	f := &memoryReaderFake{p: state.AppMemoryPressure{Episodes: 2}, recorded: true}
	var e Evidence
	collectMemory(t.Context(), f, &e)
	if !e.Memory.Known || !e.Memory.Recorded || e.Memory.Pressure.Episodes != 2 {
		t.Fatalf("memory = %+v", e.Memory)
	}
	f.err = errors.New("down")
	e = Evidence{}
	collectMemory(t.Context(), f, &e)
	if e.Memory.Known {
		t.Fatal("a failed read looked like no pressure")
	}
}
