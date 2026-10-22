package sched_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// The freeze decision treated a function instance as "warm capture
// pending" without the request-count and age gates the capture applies, so
// an instance that served fewer than warm_snapshot_min_requests requests
// parked on every idle period and never froze (Hunt 12 H12-52: h12-fnrs,
// 1 request per wake, parked three times). The shared predicate closes in
// exactly the cases the capture refuses.
func TestWarmCaptureInstanceGates(t *testing.T) {
	now := time.Date(2026, 10, 22, 17, 30, 0, 0, time.UTC)
	ready := now.Add(-30 * time.Second)
	justReady := now.Add(-500 * time.Millisecond)
	app := state.App{Type: "function", Runtime: "binary", WarmSnapshotEnabled: true, WarmSnapshotMinMs: 2000, WarmSnapshotMinRequests: 5}
	tests := []struct {
		name string
		app  state.App
		ins  state.Instance
		want bool
	}{
		{"one request, below the floor", app, state.Instance{FrameworkReadyAt: &ready, RequestCount: 1}, false},
		{"five requests", app, state.Instance{FrameworkReadyAt: &ready, RequestCount: 5}, true},
		{"ready too recently", app, state.Instance{FrameworkReadyAt: &justReady, RequestCount: 9}, false},
		{"never framework-ready", app, state.Instance{RequestCount: 9}, false},
		{"no request floor", func() state.App { a := app; a.WarmSnapshotMinRequests = 0; return a }(), state.Instance{FrameworkReadyAt: &ready}, true},
		{"before-checkpoint hook on a binary function", func() state.App {
			a := app
			a.Manifest.BeforeCheckpoint = &api.BeforeCheckpointHook{}
			return a
		}(), state.Instance{FrameworkReadyAt: &ready, RequestCount: 9}, false},
		{"profiling", func() state.App {
			a := app
			a.Manifest.Profiling = &api.ProfilingConfig{Enabled: true}
			return a
		}(), state.Instance{FrameworkReadyAt: &ready, RequestCount: 9}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sched.WarmCaptureInstanceGatesOpen(tt.app, tt.ins, now); got != tt.want {
				t.Fatalf("gates open = %v, want %v", got, tt.want)
			}
		})
	}
}
