// adr: 20261104085150 — an instance writes at most its plan's log bytes per
// UTC hour; past it the hour's output is dropped after one notice line.
package logbuf

import (
	"strings"
	"testing"
	"time"
)

func TestRingHourlyByteAllowance(t *testing.T) {
	r := New(0)
	r.SetHourlyByteAllowance(10)
	hour := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	r.mu.Lock()
	for _, line := range []string{"aaaa", "bbbb", "cccc", "dddd"} {
		r.commitLocked("stdout", line, hour.Add(time.Minute))
	}
	r.mu.Unlock()
	got := r.Snapshot(1)
	if len(got) != 3 || got[0].Line != "aaaa" || got[1].Line != "bbbb" || got[2].Line != AllowanceNotice {
		t.Fatalf("lines within the hour = %+v; want two lines and one notice", got)
	}
	if drops := r.AllowanceDrops(); drops != 2 {
		t.Fatalf("drops = %d, want 2", drops)
	}
	// The next hour starts a fresh allowance.
	r.mu.Lock()
	r.commitLocked("stdout", "eeee", hour.Add(time.Hour))
	r.mu.Unlock()
	if got := r.Snapshot(1); got[len(got)-1].Line != "eeee" || got[len(got)-1].Seq != 4 {
		t.Fatalf("next hour's line = %+v, want eeee as seq 4", got[len(got)-1])
	}
}

func TestRingWithoutAllowanceKeepsEveryLine(t *testing.T) {
	r := New(0)
	r.mu.Lock()
	for range 100 {
		r.commitLocked("stdout", strings.Repeat("x", 100), time.Now())
	}
	r.mu.Unlock()
	if got := len(r.Snapshot(1)); got != 100 || r.AllowanceDrops() != 0 {
		t.Fatalf("lines = %d drops = %d; want every line kept", got, r.AllowanceDrops())
	}
}
