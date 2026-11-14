// adr: 20261107122928 — the idle ladder's activity guard calls an app that
// quiet pause does not pause idle only when nothing is in flight and its VM
// CPU and veth traffic stayed under the guard's budget for a full window. In
// observe mode it records the pauses it would make and what would end them,
// and pauses nothing.
package fcvm

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

type fakeGuardCounters struct {
	mu      sync.Mutex
	cpu     map[string]float64
	egress  map[string]uint64
	ingress map[string]uint64
}

func newFakeGuardCounters() *fakeGuardCounters {
	return &fakeGuardCounters{cpu: map[string]float64{}, egress: map[string]uint64{}, ingress: map[string]uint64{}}
}

func (f *fakeGuardCounters) CPUSeconds(id string) (float64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.cpu[id]
	return v, ok
}

func (f *fakeGuardCounters) NetBytes(id string) (uint64, uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.egress[id]
	return e, f.ingress[id], ok
}

// add moves an instance's counters by cpu seconds and egress/ingress bytes.
func (f *fakeGuardCounters) add(id string, cpu float64, egress, ingress uint64) {
	f.mu.Lock()
	f.cpu[id] += cpu
	f.egress[id] += egress
	f.ingress[id] += ingress
	f.mu.Unlock()
}

type guardHarness struct {
	m       *Manager
	metrics *IdleLadderMetrics
	act     *fakeQuietActivity
	ctr     *fakeGuardCounters
	t0      time.Time
}

// newGuardHarness wakes "w", a request-serving app quiet pause does not
// pause, with the idle ladder observing and the guard's counters wired. The
// app's last request ended an hour ago.
func newGuardHarness(t *testing.T, quietPauseAfter time.Duration) *guardHarness {
	t.Helper()
	act, ctr := newFakeQuietActivity(), newFakeGuardCounters()
	metrics := NewIdleLadderMetrics(prometheus.NewRegistry())
	m := newTestManager(&fakeRunner{}, &residentVMM{fakeVMM: &fakeVMM{}, bytes: 64 << 20}).
		WithIdleLadder(IdleLadderConfig{Activity: act, Metrics: metrics, CPU: ctr, Net: ctr})
	req := admissionWake("w")
	req.IdleLadderEligible, req.QuietPauseAfter = true, quietPauseAfter
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "w") })
	t0 := time.Now()
	act.set("w", 0, t0.Add(-time.Hour))
	ctr.add("w", 1, 1000, 1000)
	return &guardHarness{m: m, metrics: metrics, act: act, ctr: ctr, t0: t0}
}

func (h *guardHarness) pass(after time.Duration) { h.m.idleLadderPass(h.t0.Add(after)) }

func (h *guardHarness) resumes(cause string) float64 {
	return testutil.ToFloat64(h.metrics.guardResumes.WithLabelValues(cause))
}

func (h *guardHarness) verdict(v idleGuardVerdict) int { return h.m.IdleGuardVerdicts()[v] }

func TestIdleGuardWouldPauseAQuietAppUntilARequest(t *testing.T) {
	h := newGuardHarness(t, 0)
	h.pass(0)
	h.pass(500 * time.Millisecond)
	if got := testutil.ToFloat64(h.metrics.guardPauses); got != 0 {
		t.Fatalf("pauses = %v before a full window, want 0", got)
	}
	h.pass(time.Second)
	if got := testutil.ToFloat64(h.metrics.guardPauses); got != 1 || h.verdict(idleGuardPaused) != 1 {
		t.Fatalf("pauses = %v, paused instances = %d after 1 s quiet, want 1 and 1", got, h.verdict(idleGuardPaused))
	}
	h.pass(2 * time.Second)
	// A 128 MB hobby VM, 136 MB billed, held paused for one 1 s step.
	if got := testutil.ToFloat64(h.metrics.guardHeld.WithLabelValues("hobby")); got != 136 {
		t.Fatalf("held MB-seconds = %v, want 136", got)
	}
	h.act.set("w", 0, h.t0.Add(2500*time.Millisecond))
	h.pass(3 * time.Second)
	if h.resumes(idleGuardEndRequest) != 1 {
		t.Fatalf("request resumes = %v, want 1", h.resumes(idleGuardEndRequest))
	}
	var d dto.Metric
	if err := h.metrics.guardDurations.Write(&d); err != nil {
		t.Fatal(err)
	}
	if got := d.GetHistogram().GetSampleSum(); d.GetHistogram().GetSampleCount() != 1 || got != 2 {
		t.Fatalf("pause durations = %d samples, sum %v; want one 2 s pause", d.GetHistogram().GetSampleCount(), got)
	}
}

func TestIdleGuardKeepsBusyAppsRunning(t *testing.T) {
	for _, tc := range []struct {
		name            string
		cpu             float64
		egress, ingress uint64
		want            idleGuardVerdict
	}{
		{name: "timer work", cpu: 0.050, want: idleGuardCPU},
		{name: "outbound traffic", egress: 4096, want: idleGuardNet},
		{name: "inbound traffic", ingress: 4096, want: idleGuardNet},
		{name: "idle ticks and an ARP", cpu: 0.002, egress: 60, ingress: 60, want: idleGuardPaused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newGuardHarness(t, 0)
			h.pass(0)
			h.ctr.add("w", tc.cpu, tc.egress, tc.ingress)
			h.pass(time.Second)
			if h.verdict(tc.want) != 1 {
				t.Fatalf("verdicts = %v, want one at %v", h.m.IdleGuardVerdicts(), idleGuardVerdictNames[tc.want])
			}
			if paused := testutil.ToFloat64(h.metrics.guardPauses); (tc.want == idleGuardPaused) != (paused == 1) {
				t.Fatalf("pauses = %v for verdict %v", paused, idleGuardVerdictNames[tc.want])
			}
		})
	}
}

// Traffic to the guest ends a pause the way a real one would resume, and work
// the app starts on its own ends it as background; the guard pauses again once
// a quiet window follows.
func TestIdleGuardRecordsWhatWouldEndAPause(t *testing.T) {
	h := newGuardHarness(t, 0)
	h.pass(0)
	h.pass(time.Second)
	h.ctr.add("w", 0, 0, 2048)
	h.pass(1500 * time.Millisecond)
	if h.resumes(idleGuardEndInbound) != 1 {
		t.Fatalf("inbound resumes = %v, want 1", h.resumes(idleGuardEndInbound))
	}
	h.pass(2500 * time.Millisecond)
	if got := testutil.ToFloat64(h.metrics.guardPauses); got != 2 {
		t.Fatalf("pauses = %v after a quiet window, want 2", got)
	}
	h.ctr.add("w", 0.2, 0, 0)
	h.pass(3 * time.Second)
	if h.resumes(idleGuardEndBackground) != 1 {
		t.Fatalf("background resumes = %v, want 1", h.resumes(idleGuardEndBackground))
	}
}

// Functions and between_requests apps are quiet pause's; the guard leaves them
// alone. Without both counter sources the guard is off.
func TestIdleGuardScope(t *testing.T) {
	h := newGuardHarness(t, 2*time.Second)
	h.pass(0)
	h.pass(5 * time.Second)
	if got := testutil.ToFloat64(h.metrics.guardPauses); got != 0 || h.verdict(idleGuardNone) != 1 {
		t.Fatalf("quiet-pause instance: pauses = %v, verdicts %v; want none", got, h.m.IdleGuardVerdicts())
	}

	act := newFakeQuietActivity()
	metrics := NewIdleLadderMetrics(prometheus.NewRegistry())
	m := newTestManager(&fakeRunner{}, &residentVMM{fakeVMM: &fakeVMM{}}).
		WithIdleLadder(IdleLadderConfig{Activity: act, Metrics: metrics, CPU: newFakeGuardCounters()})
	req := admissionWake("x")
	req.IdleLadderEligible = true
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "x") })
	now := time.Now()
	m.idleLadderPass(now)
	m.idleLadderPass(now.Add(5 * time.Second))
	if got := testutil.ToFloat64(metrics.guardPauses); got != 0 {
		t.Fatalf("guard without a net source paused %v times, want 0", got)
	}
}

// A counter reset (a veth recreated) or a missing reading leaves the guard
// without a full window: it neither pauses nor calls the app busy.
func TestIdleGuardWaitsForAFullWindow(t *testing.T) {
	h := newGuardHarness(t, 0)
	h.pass(0)
	h.ctr.mu.Lock()
	h.ctr.egress["w"] = 0
	h.ctr.mu.Unlock()
	h.pass(time.Second)
	if h.verdict(idleGuardUnknown) != 1 || testutil.ToFloat64(h.metrics.guardPauses) != 0 {
		t.Fatalf("after a counter reset: verdicts %v, pauses %v; want unknown and none",
			h.m.IdleGuardVerdicts(), testutil.ToFloat64(h.metrics.guardPauses))
	}
	h.ctr.mu.Lock()
	delete(h.ctr.cpu, "w")
	h.ctr.mu.Unlock()
	h.pass(3 * time.Second)
	if h.verdict(idleGuardUnknown) != 1 {
		t.Fatalf("without a CPU reading: verdicts %v, want unknown", h.m.IdleGuardVerdicts())
	}
}
