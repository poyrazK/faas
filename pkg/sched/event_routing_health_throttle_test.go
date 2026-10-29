// adr: 614 — a node refreshes the routing health gauges at most every 15 s;
// the read aggregates every unsettled receipt on the platform.
package sched_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventRoutingHealthIsReadAtMostEvery15Seconds(t *testing.T) {
	engine := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := time.Now()
	for _, step := range []struct {
		after time.Duration
		want  bool
	}{
		{0, true},
		{time.Second, false},
		{14 * time.Second, false},
		{15 * time.Second, true},
		{16 * time.Second, false},
		{31 * time.Second, true},
	} {
		if got := loop.XEventRoutingHealthDue(start.Add(step.after)); got != step.want {
			t.Fatalf("health due at +%s = %v, want %v", step.after, got, step.want)
		}
	}
}
