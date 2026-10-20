package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"log/slog"
	"testing"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(sched.DiscardWriter{}, nil)) }

// This file drives the 0%-coverage *Loop With* setters. Most are
// pure delegation: they return the loop pointer for chaining and
// set the corresponding field. The test exercises each setter
// and asserts the loop pointer is returned (chainable).

func TestLoopSetSweep_WithHeartbeat(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithHeartbeat(&sched.Heartbeat{}); got != l {
		t.Error("WithHeartbeat returned different pointer")
	}
}

func TestLoopSetSweep_WithDiskDrift(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithDiskDrift(&sched.DiskDrift{}); got != l {
		t.Error("WithDiskDrift returned different pointer")
	}
}

func TestLoopSetSweep_WithRetention(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithRetention(nil); got != l {
		t.Error("WithRetention returned different pointer")
	}
}

func TestLoopSetSweep_WithScaleUp(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithScaleUp(nil); got != l {
		t.Error("WithScaleUp returned different pointer")
	}
}

func TestLoopSetSweep_WithTargets(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithTargets(nil); got != l {
		t.Error("WithTargets returned different pointer")
	}
}

func TestLoopSetSweep_WithFloor(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithFloor(nil); got != l {
		t.Error("WithFloor returned different pointer")
	}
}

func TestLoopSetSweep_WithReaperParkCap(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithReaperParkCap(5); got != l {
		t.Error("WithReaperParkCap returned different pointer")
	}
}

func TestLoopSetSweep_WithLivenessWindow(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithLivenessWindow(&sched.LivenessWindow{}); got != l {
		t.Error("WithLivenessWindow returned different pointer")
	}
}

func TestLoopSetSweep_WithMigratingWatchdog(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithMigratingWatchdog(nil); got != l {
		t.Error("WithMigratingWatchdog returned different pointer")
	}
}

func TestLoopSetSweep_WithRecentLoad(t *testing.T) {
	l := sched.NewLoop(nil, nil, discardLog())
	if got := l.WithRecentLoad(nil); got != l {
		t.Error("WithRecentLoad returned different pointer")
	}
}
