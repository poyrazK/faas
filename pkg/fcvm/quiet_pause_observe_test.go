// adr: 1151 — observe mode measures the pauses quiet pause would make on a
// node and pauses nothing: probes, admission and the pause ledger see a
// running instance.
package fcvm

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// newObservingManager wakes instance "q" with quiet pause in observe mode,
// as if the pass first saw it an hour ago.
func newObservingManager(t *testing.T, v *pausingVMM, act QuietActivity) (*Manager, *QuietPauseMetrics) {
	t.Helper()
	metrics := NewQuietPauseMetrics(prometheus.NewRegistry())
	m := newTestManager(&fakeRunner{}, v).WithQuietPause(QuietPauseConfig{Activity: act, Metrics: metrics, Observe: true})
	if _, err := m.Wake(t.Context(), quietWake("q")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "q") })
	seenQuiet(m, "q", time.Now().Add(-time.Hour))
	return m, metrics
}

func TestQuietObserveMeasuresWithoutPausing(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m, metrics := newObservingManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	_, epoch0 := m.QuietGate("q")

	m.quietPausePass(t.Context(), time.Now())
	m.quietPausePass(t.Context(), time.Now())
	if v.pauses() != 0 {
		t.Fatalf("observe mode paused the guest %d times", v.pauses())
	}
	if got := testutil.ToFloat64(metrics.observedPauses); got != 1 {
		t.Fatalf("observed pauses = %v, want 1 across two passes", got)
	}
	if got := testutil.ToFloat64(metrics.observed); got != 1 {
		t.Fatalf("observed instances = %v, want 1", got)
	}
	// Nothing may treat an observed instance as paused.
	if paused, epoch := m.QuietGate("q"); paused || epoch != epoch0 {
		t.Fatalf("QuietGate = %v, epoch %d -> %d; probes must see a running guest", paused, epoch0, epoch)
	}
	if r := m.QuietPauseReport("q"); r != (QuietPauseReport{}) {
		t.Fatalf("QuietPauseReport = %+v, want none: admission and the ledger must not see a pause", r)
	}

	// The next request ends the observed pause where the guest would have
	// resumed, without calling Firecracker.
	time.Sleep(10 * time.Millisecond)
	if err := m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if v.resumes() != 0 {
		t.Fatalf("observe mode resumed the guest %d times", v.resumes())
	}
	var durations dto.Metric
	if err := metrics.observedDurations.Write(&durations); err != nil {
		t.Fatal(err)
	}
	if n := durations.GetHistogram().GetSampleCount(); n != 1 {
		t.Fatalf("observed pause durations = %d, want 1", n)
	}
	mbSeconds := testutil.ToFloat64(metrics.observedMBSeconds.WithLabelValues("hobby"))
	if mbSeconds < 136*0.01 || mbSeconds > 136*60 {
		t.Fatalf("observed MB-seconds = %v, want the 136 MB VM times the pause", mbSeconds)
	}
	if err := m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.observedMBSeconds.WithLabelValues("hobby")); got != mbSeconds {
		t.Fatalf("a second QuietResume added MB-seconds: %v -> %v", mbSeconds, got)
	}
	act.set("q", 1, time.Now())
	m.quietPausePass(t.Context(), time.Now())
	if got := testutil.ToFloat64(metrics.observed); got != 0 {
		t.Fatalf("observed instances = %v after the request, want 0", got)
	}
}

// An instance with a request in flight, or a recent one, is not observed.
func TestQuietObserveSkipsBusyInstances(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m, metrics := newObservingManager(t, v, act)
	act.set("q", 1, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	act.set("q", 0, time.Now())
	m.quietPausePass(t.Context(), time.Now())
	if got := testutil.ToFloat64(metrics.observedPauses); got != 0 {
		t.Fatalf("observed pauses = %v, want 0 while busy or within QuietPauseAfter", got)
	}
}
