// adr: 20261023171905 — vmmd reports each completed quiet pause and the
// incarnation's paused total, the input of the pause ledger billing reads.
package fcvm

import (
	"errors"
	"testing"
	"time"
)

func TestQuietPauseReportRecordsCompletedPauses(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	if r := m.QuietPauseReport("q"); r != (QuietPauseReport{}) {
		t.Fatalf("report before any pause = %+v, want zero", r)
	}

	var total time.Duration
	for i := 1; i <= 2; i++ {
		m.quietPausePass(t.Context(), time.Now())
		open := m.QuietPauseReport("q")
		if !open.Paused || open.Since.IsZero() {
			t.Fatalf("pause %d: report = %+v, want an open pause", i, open)
		}
		if open.PausedTotal != total {
			t.Fatalf("pause %d: an open pause counted in the total: %s, want %s", i, open.PausedTotal, total)
		}
		if err := m.QuietResume(t.Context(), "q"); err != nil {
			t.Fatal(err)
		}
		done := m.QuietPauseReport("q")
		if done.Paused || !done.Since.IsZero() {
			t.Fatalf("pause %d: report after resume = %+v, want no open pause", i, done)
		}
		if !done.LastStarted.Equal(open.Since) || done.LastEnded.Before(done.LastStarted) {
			t.Fatalf("pause %d: last pause = [%v, %v], want it to start at %v", i, done.LastStarted, done.LastEnded, open.Since)
		}
		total += done.LastEnded.Sub(done.LastStarted)
		if done.PausedTotal != total {
			t.Fatalf("pause %d: paused total = %s, want %s", i, done.PausedTotal, total)
		}
	}
	if v.pauses() != 2 || v.resumes() != 2 {
		t.Fatalf("pauses=%d resumes=%d, want 2 each", v.pauses(), v.resumes())
	}

	if err := m.Destroy(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), quietWake("q")); err != nil {
		t.Fatal(err)
	}
	if r := m.QuietPauseReport("q"); r != (QuietPauseReport{}) {
		t.Fatalf("new incarnation reported the old one's pauses: %+v", r)
	}
}

// A resume that fails leaves the pause open: no completed pause is
// recorded for a VM that never ran again.
func TestQuietPauseReportKeepsFailedResumeOpen(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	boom := errors.New("boom")
	v.fakeVMM.mu.Lock()
	v.fakeVMM.resumeErr = boom
	v.fakeVMM.mu.Unlock()
	if err := m.QuietResume(t.Context(), "q"); !errors.Is(err, boom) {
		t.Fatalf("QuietResume err = %v, want %v", err, boom)
	}
	r := m.QuietPauseReport("q")
	if !r.Paused || !r.LastStarted.IsZero() || r.PausedTotal != 0 {
		t.Fatalf("report after a failed resume = %+v, want the pause still open and none completed", r)
	}
}
