// adr: 20261107122928 — with idle_ladder_apps on, quiet pause pauses an app
// that declared no pause only when the activity guard agrees it is idle, and
// vmmd resumes it on any packet addressed to it while it is paused, or, without
// that reading, on any traffic that reaches its veth (amendment 4).
package fcvm

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type appPauseHarness struct {
	m       *Manager
	v       *pausingVMM
	metrics *IdleLadderMetrics
	act     *fakeQuietActivity
	ctr     *fakeGuardCounters
	t0      time.Time
}

// newAppPauseHarness wakes "a", an app schedd marked for the guarded pause,
// with quiet pause and the idle ladder on. Its last request ended an hour ago.
func newAppPauseHarness(t *testing.T, apps, marked bool) *appPauseHarness {
	t.Helper()
	act, ctr := newFakeQuietActivity(), newFakeGuardCounters()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	metrics := NewIdleLadderMetrics(prometheus.NewRegistry())
	m := newTestManager(&fakeRunner{}, v).
		WithQuietPause(QuietPauseConfig{Activity: act}).
		WithIdleLadder(IdleLadderConfig{Activity: act, Metrics: metrics, On: true, Apps: apps, CPU: ctr, Net: ctr})
	req := admissionWake("a")
	req.IdleLadderEligible, req.IdleLadderGuardedPause = true, marked
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "a") })
	seenQuiet(m, "a", time.Now().Add(-time.Hour))
	t0 := time.Now()
	act.set("a", 0, t0.Add(-time.Hour))
	ctr.add("a", 1, 1000, 1000)
	return &appPauseHarness{m: m, v: v, metrics: metrics, act: act, ctr: ctr, t0: t0}
}

// quietWindow runs the ladder pass over one full quiet window, then the
// quiet pause pass.
func (h *appPauseHarness) quietWindow(t *testing.T) {
	t.Helper()
	h.m.idleLadderPass(h.t0)
	h.m.idleLadderPass(h.t0.Add(time.Second))
	h.m.quietPausePass(t.Context(), h.t0.Add(time.Second))
}

func TestGuardedAppPausesOnlyWhenTheGuardAgrees(t *testing.T) {
	for _, tc := range []struct {
		name         string
		apps, marked bool
		work         func(*fakeGuardCounters)
		wantPause    bool
	}{
		{name: "quiet app", apps: true, marked: true, wantPause: true},
		{name: "timer work", apps: true, marked: true, work: func(c *fakeGuardCounters) { c.add("a", 0.05, 0, 0) }},
		{name: "outbound traffic", apps: true, marked: true, work: func(c *fakeGuardCounters) { c.add("a", 0, 4096, 0) }},
		{name: "switch off", marked: true},
		{name: "app declared a pause", apps: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAppPauseHarness(t, tc.apps, tc.marked)
			h.m.idleLadderPass(h.t0)
			if tc.work != nil {
				tc.work(h.ctr)
			}
			h.m.idleLadderPass(h.t0.Add(time.Second))
			h.m.quietPausePass(t.Context(), h.t0.Add(time.Second))
			if got := h.v.pauses() == 1; got != tc.wantPause {
				t.Fatalf("paused = %v, want %v", got, tc.wantPause)
			}
		})
	}
}

// Stale counters (the ladder pass stopped) never allow a pause.
func TestGuardedAppWaitsForFreshCounters(t *testing.T) {
	h := newAppPauseHarness(t, true, true)
	h.m.idleLadderPass(h.t0)
	h.m.idleLadderPass(h.t0.Add(time.Second))
	h.m.quietPausePass(t.Context(), h.t0.Add(3*time.Second))
	if h.v.pauses() != 0 {
		t.Fatalf("paused on 2 s old counters")
	}
}

// Without an addressed reading, any veth ingress resumes the paused app.
func TestPausedGuardedAppResumesOnVethTrafficWithoutAddressedReading(t *testing.T) {
	h := newAppPauseHarness(t, true, true)
	h.quietWindow(t)
	if h.v.pauses() != 1 {
		t.Fatalf("pauses = %d, want 1", h.v.pauses())
	}
	if r := h.m.idleLadderPass(h.t0.Add(1250 * time.Millisecond)); len(r) != 0 {
		t.Fatalf("resumes without traffic = %v, want none", r)
	}
	h.ctr.add("a", 0, 0, 66) // one TCP keepalive
	resumes := h.m.idleLadderPass(h.t0.Add(1500 * time.Millisecond))
	if len(resumes) != 1 || resumes[0] != (idleAppResume{id: "a", cause: idleAppResumeInboundVeth}) {
		t.Fatalf("resumes = %v, want a for inbound_veth", resumes)
	}
	for _, r := range resumes {
		h.m.idleInboundResume(t.Context(), r)
	}
	if h.v.resumes() != 1 {
		t.Fatalf("guest resumes = %d, want 1", h.v.resumes())
	}
	if got := testutil.ToFloat64(h.metrics.appResumes.WithLabelValues(idleAppResumeInboundVeth)); got != 1 {
		t.Fatalf("inbound_veth resumes metric = %v, want 1", got)
	}
	if r := h.m.idleLadderPass(h.t0.Add(1750 * time.Millisecond)); len(r) != 0 {
		t.Fatalf("resumes after the guest runs = %v, want none", r)
	}
}

// With an addressed reading, the bridge's broadcasts and floods that move only
// the veth leave the app paused; a packet addressed to it resumes it.
func TestPausedGuardedAppResumesOnlyOnAddressedPackets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		traffic    func(*fakeGuardCounters)
		wantResume bool
		wantCause  string
	}{
		{name: "no traffic"},
		{name: "ARP broadcast", traffic: func(c *fakeGuardCounters) { c.add("a", 0, 0, 42) }},
		{name: "unicast flooded for another instance", traffic: func(c *fakeGuardCounters) { c.add("a", 0, 0, 74) }},
		{name: "reply on the app's connection", traffic: func(c *fakeGuardCounters) {
			c.add("a", 0, 0, 66)
			c.addAddressed("a", 1)
		}, wantResume: true, wantCause: idleAppResumeInbound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAppPauseHarness(t, true, true)
			h.ctr.addAddressed("a", 500)
			h.quietWindow(t)
			if h.v.pauses() != 1 {
				t.Fatalf("pauses = %d, want 1", h.v.pauses())
			}
			if tc.traffic != nil {
				tc.traffic(h.ctr)
			}
			resumes := h.m.idleLadderPass(h.t0.Add(1500 * time.Millisecond))
			if got := len(resumes) == 1; got != tc.wantResume {
				t.Fatalf("resumes = %v, want resume %v", resumes, tc.wantResume)
			}
			if tc.wantResume && resumes[0] != (idleAppResume{id: "a", cause: tc.wantCause}) {
				t.Fatalf("resumes = %v, want a for %s", resumes, tc.wantCause)
			}
		})
	}
}

// An addressed reading lost during the pause falls back to the veth bytes.
func TestPausedGuardedAppFallsBackToVethWhenAddressedReadingGoes(t *testing.T) {
	h := newAppPauseHarness(t, true, true)
	h.ctr.addAddressed("a", 500)
	h.quietWindow(t)
	h.ctr.mu.Lock()
	delete(h.ctr.addressed, "a")
	h.ctr.mu.Unlock()
	if r := h.m.idleLadderPass(h.t0.Add(1250 * time.Millisecond)); len(r) != 0 {
		t.Fatalf("resumes without traffic = %v, want none", r)
	}
	h.ctr.add("a", 0, 0, 42)
	resumes := h.m.idleLadderPass(h.t0.Add(1500 * time.Millisecond))
	if len(resumes) != 1 || resumes[0].cause != idleAppResumeInboundVeth {
		t.Fatalf("resumes = %v, want a for inbound_veth", resumes)
	}
}

// A paused app whose counters vanish is resumed: vmmd cannot watch it.
func TestPausedGuardedAppResumesWithoutCounters(t *testing.T) {
	h := newAppPauseHarness(t, true, true)
	h.quietWindow(t)
	h.ctr.mu.Lock()
	delete(h.ctr.egress, "a")
	h.ctr.mu.Unlock()
	resumes := h.m.idleLadderPass(h.t0.Add(1500 * time.Millisecond))
	if len(resumes) != 1 || resumes[0].cause != idleAppResumeNoCounters {
		t.Fatalf("resumes = %v, want a for no_counters", resumes)
	}
}
