// adr: 20261107122928 — the idle ladder's observe mode measures, for every
// request-serving instance whatever its declared pause, the level its idle
// time reaches, the memory held at each level, the level each request wakes
// from and the billed MB-seconds the unbilled levels would not bill. It
// pauses nothing.
package fcvm

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestIdleLevelFor(t *testing.T) {
	for _, tc := range []struct {
		idle time.Duration
		want IdleLevel
	}{
		{0, IdleRunning},
		{999 * time.Millisecond, IdleRunning},
		{time.Second, IdleQuietPause},
		{9999 * time.Millisecond, IdleQuietPause},
		{10 * time.Second, IdleFreeze},
		{59 * time.Second, IdleFreeze},
		{time.Minute, IdleCold1},
		{179 * time.Second, IdleCold1},
		{3 * time.Minute, IdleCold2},
		{599 * time.Second, IdleCold2},
		{10 * time.Minute, IdleSnapshot},
		{2 * time.Hour, IdleSnapshot},
	} {
		if got := IdleLevelFor(tc.idle); got != tc.want {
			t.Errorf("IdleLevelFor(%v) = %v, want %v", tc.idle, got, tc.want)
		}
	}
}

func TestIdleLadderEligibleFor(t *testing.T) {
	base := admissionWake("w")
	base.IdleLadderEligible = true
	for _, tc := range []struct {
		name          string
		edit          func(*WakeRequest)
		qualification bool
		want          bool
	}{
		{name: "request-serving app", edit: func(*WakeRequest) {}, want: true},
		{name: "quiet pause eligible from an older schedd", edit: func(w *WakeRequest) {
			w.IdleLadderEligible, w.QuietPauseAfter = false, 2*time.Second
		}, want: true},
		{name: "not request-serving", edit: func(w *WakeRequest) { w.IdleLadderEligible = false }},
		{name: "execution", edit: func(w *WakeRequest) { w.ExecutionOnly = true }},
		{name: "app task", edit: func(w *WakeRequest) { w.AppTaskOnly = true }},
		{name: "qualification", edit: func(*WakeRequest) {}, qualification: true},
		{name: "warm pool", edit: func(w *WakeRequest) { w.KeepPaused = true }},
		{name: "export", edit: func(w *WakeRequest) { w.ExportDir = "/srv/export" }},
		{name: "private network", edit: func(w *WakeRequest) { w.PrivateNetworkID = "net-1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.edit(&req)
			if got := idleLadderEligibleFor(req, tc.qualification); got != tc.want {
				t.Fatalf("idleLadderEligibleFor = %v, want %v", got, tc.want)
			}
		})
	}
}

// residentVMM reports a fixed memory.current for every VM.
type residentVMM struct {
	*fakeVMM
	bytes int64
}

func (v *residentVMM) VMResidentBytes(Lease) (int64, error) { return v.bytes, nil }

// newLadderManager wakes instance "w", a request-serving app that is not
// quiet-paused, with the idle ladder in observe mode.
func newLadderManager(t *testing.T, act QuietActivity) (*Manager, *IdleLadderMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	metrics := NewIdleLadderMetrics(reg)
	v := &residentVMM{fakeVMM: &fakeVMM{}, bytes: 100 << 20}
	m := newTestManager(&fakeRunner{}, v).WithIdleLadderObserve(IdleLadderConfig{Activity: act, Metrics: metrics})
	req := admissionWake("w")
	req.IdleLadderEligible = true
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "w") })
	return m, metrics, reg
}

func wakesAt(metrics *IdleLadderMetrics, level IdleLevel) float64 {
	return testutil.ToFloat64(metrics.wakes.WithLabelValues(level.String()))
}

func TestIdleLadderObserveRecordsLevelsAndWakes(t *testing.T) {
	act := newFakeQuietActivity()
	m, metrics, reg := newLadderManager(t, act)
	t0 := time.Now()
	act.set("w", 0, t0.Add(-time.Hour))

	m.idleLadderPass(t0) // first sight: idle is measured from here
	m.idleLadderPass(t0.Add(90 * time.Second))
	counts, resident := m.IdleLadderLevels()
	if counts[IdleCold1] != 1 || resident[IdleCold1] != 100<<20 {
		t.Fatalf("levels = %v / %v, want one 100 MiB instance at cold_1", counts, resident)
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP vmmd_idle_ladder_observed_instances Request-serving instances on this node by the idle ladder level their idle time reaches (ADR-20261107122928, observe mode).
# TYPE vmmd_idle_ladder_observed_instances gauge
vmmd_idle_ladder_observed_instances{level="cold_1"} 1
vmmd_idle_ladder_observed_instances{level="cold_2"} 0
vmmd_idle_ladder_observed_instances{level="freeze"} 0
vmmd_idle_ladder_observed_instances{level="quiet_pause"} 0
vmmd_idle_ladder_observed_instances{level="running"} 0
vmmd_idle_ladder_observed_instances{level="snapshot"} 0
`), "vmmd_idle_ladder_observed_instances"); err != nil {
		t.Fatal(err)
	}
	// One pass credits at most idleLadderMaxStep of billed idle time: a
	// 128 MB hobby VM, 136 MB billed, for 5 s.
	if got := testutil.ToFloat64(metrics.unbilled.WithLabelValues("hobby")); got != 136*5 {
		t.Fatalf("unbilled MB-seconds = %v, want %v", got, 136*5)
	}

	// A request came and went between passes: the gap ends at the level it
	// reached, and a new one starts after the request.
	act.set("w", 0, t0.Add(91*time.Second))
	m.idleLadderPass(t0.Add(92 * time.Second))
	if wakesAt(metrics, IdleCold1) != 1 {
		t.Fatalf("cold_1 wakes = %v, want 1", wakesAt(metrics, IdleCold1))
	}
	var gaps dto.Metric
	if err := metrics.gaps.Write(&gaps); err != nil {
		t.Fatal(err)
	}
	if h := gaps.GetHistogram(); h.GetSampleCount() != 1 || h.GetSampleSum() != 90 {
		t.Fatalf("gaps = %d samples, sum %v; want one 90 s gap", h.GetSampleCount(), h.GetSampleSum())
	}
	// The new gap is 1 s old: quiet pause, and nothing past it to unbill.
	if got := testutil.ToFloat64(metrics.unbilled.WithLabelValues("hobby")); got != 136*5 {
		t.Fatalf("unbilled MB-seconds = %v after the request, want %v", got, 136*5)
	}

	// A request in flight ends the 1 s gap.
	act.set("w", 1, t0.Add(91*time.Second))
	m.idleLadderPass(t0.Add(93 * time.Second))
	if wakesAt(metrics, IdleQuietPause) != 1 {
		t.Fatalf("quiet_pause wakes = %v, want 1", wakesAt(metrics, IdleQuietPause))
	}
	if counts, _ := m.IdleLadderLevels(); counts[IdleRunning] != 1 {
		t.Fatalf("levels = %v while busy, want running", counts)
	}
}

// Gaps shorter than the quiet pause level are not wakes, and a busy instance
// is never credited idle time.
func TestIdleLadderObserveIgnoresShortGapsAndBusyTime(t *testing.T) {
	act := newFakeQuietActivity()
	m, metrics, _ := newLadderManager(t, act)
	t0 := time.Now()
	act.set("w", 1, t0)
	m.idleLadderPass(t0)
	m.idleLadderPass(t0.Add(30 * time.Second))
	act.set("w", 0, t0.Add(30*time.Second))
	m.idleLadderPass(t0.Add(30*time.Second + 500*time.Millisecond))
	act.set("w", 1, t0.Add(31*time.Second))
	m.idleLadderPass(t0.Add(31 * time.Second))
	for l := IdleQuietPause; l < idleLevelCount; l++ {
		if got := wakesAt(metrics, l); got != 0 {
			t.Fatalf("%v wakes = %v, want 0", l, got)
		}
	}
	if got := testutil.ToFloat64(metrics.unbilled.WithLabelValues("hobby")); got != 0 {
		t.Fatalf("unbilled MB-seconds = %v, want 0", got)
	}
}

// A frozen instance is measured at its level but is already unbilled.
func TestIdleLadderObserveFrozenIsNotUnbilledAgain(t *testing.T) {
	act := newFakeQuietActivity()
	m, metrics, _ := newLadderManager(t, act)
	m.mu.Lock()
	m.live["w"].Frozen = true
	m.mu.Unlock()
	t0 := time.Now()
	act.set("w", 0, t0.Add(-time.Hour))
	m.idleLadderPass(t0)
	m.idleLadderPass(t0.Add(15 * time.Second))
	if counts, _ := m.IdleLadderLevels(); counts[IdleFreeze] != 1 {
		t.Fatalf("levels = %v, want one instance at freeze", counts)
	}
	if got := testutil.ToFloat64(metrics.unbilled.WithLabelValues("hobby")); got != 0 {
		t.Fatalf("unbilled MB-seconds = %v for a frozen instance, want 0", got)
	}
}

// Instances schedd did not mark are not measured, and a destroyed instance
// leaves the levels.
func TestIdleLadderObserveTracksOnlyLiveEligibleInstances(t *testing.T) {
	act := newFakeQuietActivity()
	m, _, _ := newLadderManager(t, act)
	if _, err := m.Wake(t.Context(), admissionWake("other")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "other") })
	t0 := time.Now()
	m.idleLadderPass(t0)
	m.idleLadderPass(t0.Add(20 * time.Second))
	counts, _ := m.IdleLadderLevels()
	total := 0
	for _, n := range counts {
		total += n
	}
	if total != 1 || counts[IdleFreeze] != 1 {
		t.Fatalf("levels = %v, want only the marked instance, at freeze", counts)
	}
	if err := m.Destroy(t.Context(), "w"); err != nil {
		t.Fatal(err)
	}
	m.idleLadderPass(t0.Add(21 * time.Second))
	if counts, _ := m.IdleLadderLevels(); counts != ([idleLevelCount]int{}) {
		t.Fatalf("levels = %v after destroy, want none", counts)
	}
}
