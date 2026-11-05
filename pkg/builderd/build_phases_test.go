package builderd

import (
	"testing"
	"time"
)

// Production-us rc.294: a 3-file Go function spent 114 s in builderd while
// its guest build steps took 15 s, and neither the build log nor builderd's
// journal said where the rest went (Hunt 14 H14-11).
func TestBuildPhaseClockNamesWhereTheTimeWent(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 1, 27, 5, 0, time.UTC)
	c := buildPhaseClock{start: t0, vmReady: t0.Add(42 * time.Second), vmDone: t0.Add(100 * time.Second),
		published: t0.Add(104 * time.Second), completed: t0.Add(114 * time.Second)}
	if got, want := c.vmLine(), "[build] builder VM ready in 42s; build in VM 58s\n"; got != want {
		t.Errorf("vmLine = %q, want %q", got, want)
	}
	got := map[string]any{}
	attrs := c.attrs()
	for i := 0; i+1 < len(attrs); i += 2 {
		got[attrs[i].(string)] = attrs[i+1]
	}
	want := map[string]int64{"vm_start_ms": 42000, "vm_build_ms": 58000, "artifact_checks_ms": 4000, "complete_ms": 10000, "total_ms": 114000}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %d", k, got[k], v)
		}
	}
	// A phase that never started reads as zero, not as a negative duration.
	if d := phaseDuration(t0, time.Time{}); d != 0 {
		t.Errorf("unfinished phase = %s, want 0", d)
	}
}
