package sched_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// Five OOM reports in a minute for a live release cause one stale mark and
// then a wake backoff that grows while the kills continue and ends after a
// quiet window (bug hunt H34).
func TestOOMBackoffBoundsTheKillLoop(t *testing.T) {
	var b sched.OOMBackoff
	now := time.Unix(1_800_000_000, 0)
	stales := 0
	var backingOff bool
	for i := range api.WorkloadOOMBackoffKills {
		stale, off := b.Record("dep-1", now.Add(time.Duration(i)*10*time.Second))
		if stale {
			stales++
		}
		backingOff = off
		if off && i < api.WorkloadOOMBackoffKills-1 {
			t.Fatalf("backoff started after %d kills, want %d", i+1, api.WorkloadOOMBackoffKills)
		}
	}
	if stales != 1 || !backingOff {
		t.Fatalf("stale marks = %d, backing off = %v; want 1 and true", stales, backingOff)
	}
	last := now.Add(time.Duration(api.WorkloadOOMBackoffKills-1) * 10 * time.Second)
	wait, active := b.Active("dep-1", last)
	if !active || wait != api.WorkloadOOMBackoffInitial {
		t.Fatalf("backoff = %s %v, want %s", wait, active, api.WorkloadOOMBackoffInitial)
	}
	if _, active := b.Active("dep-2", last); active {
		t.Fatal("another deployment backs off")
	}
	// The next kill after the backoff doubles it.
	next := last.Add(api.WorkloadOOMBackoffInitial)
	if _, off := b.Record("dep-1", next); !off {
		t.Fatal("a kill right after the backoff did not back off again")
	}
	if wait, _ := b.Active("dep-1", next); wait != 2*api.WorkloadOOMBackoffInitial {
		t.Fatalf("second backoff = %s, want %s", wait, 2*api.WorkloadOOMBackoffInitial)
	}
	// After a quiet window, a kill re-stales the snapshots and starts over.
	quiet := next.Add(api.WorkloadOOMBackoffWindow + time.Minute)
	if _, active := b.Active("dep-1", quiet); active {
		t.Fatal("backoff outlived a quiet window")
	}
	if stale, off := b.Record("dep-1", quiet); !stale || off {
		t.Fatalf("first kill after a quiet window: stale=%v backoff=%v, want true false", stale, off)
	}
}

func TestOOMBackoffProblemNamesTheOOM(t *testing.T) {
	p := sched.ErrWorkloadOOMBackoff(29500 * time.Millisecond)
	if p.Status != http.StatusServiceUnavailable || p.Code != api.CodeAppRuntimeOOM {
		t.Fatalf("problem = %d %s, want 503 %s", p.Status, p.Code, api.CodeAppRuntimeOOM)
	}
	if got := p.HasHeader("Retry-After"); len(got) != 1 || got[0] != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
}
