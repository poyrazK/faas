//go:build metal

// adr: 1134 — wake-ahead learns private TCP edges. A caller that opens a TCP
// session to a parked internal service after each of its own cold wakes
// teaches the gateway that edge; once the caller opts in, the service is
// restored while the caller wakes instead of after it connects.
//
// The comparison alternates blocks with the opt-in on and off inside one run,
// on one gateway: a shared metal host's load drifts by minutes, and restores
// measured an hour apart differed by 3x on n2.
package e2e_test

import (
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// privateTCPWakeAheadBlocks alternate on, off, on, off, … after learning;
// each block measures privateTCPWakeAheadBlockCycles cold wakes.
const (
	privateTCPWakeAheadBlocks      = 4
	privateTCPWakeAheadBlockCycles = 4
)

// privateTCPWakeAheadMaxFailed is how many cold cycles per phase may fail
// (a slow restore on the shared metal host) before the measurement is void.
const privateTCPWakeAheadMaxFailed = 2

func TestPrivateTCPWakeAheadMetal(t *testing.T) {
	f := newPrivateTCPImageFixture(t)
	if f == nil {
		return
	}
	if got := f.dial(t, "warm-up"); !f.echoed(got, "warm-up") {
		f.h.DumpLogs(t)
		t.Fatalf("warm-up dial = %+v, want the echoed payload", got)
	}

	// Each cycle is one cold wake of the caller followed by a TCP session to
	// the parked service. Cold wakes are spaced past the follow window so
	// each counts as its own wake.
	var lastWake time.Time
	failed := map[string]int{}
	coldDial := func(phase string, i int) (time.Duration, bool) {
		t.Helper()
		f.park(t, f.service)
		f.park(t, f.caller)
		if wait := time.Until(lastWake.Add(api.ServiceWakeAheadFollowWindow + time.Second)); wait > 0 {
			time.Sleep(wait)
		}
		payload := phase + "-" + strconv.Itoa(i) + "-" + randHexSuffix()
		lastWake = time.Now()
		got := f.dial(t, payload)
		elapsed := time.Since(lastWake)
		if !f.echoed(got, payload) {
			failed[phase]++
			t.Logf("%s %d: cold dial failed after %s: %+v", phase, i, elapsed.Round(time.Millisecond), got)
			if failed[phase] > privateTCPWakeAheadMaxFailed {
				f.h.DumpLogs(t)
				t.Fatalf("%d cold dials failed in phase %s; the measurement is void", failed[phase], phase)
			}
			return 0, false
		}
		return elapsed, true
	}

	// Wake-ahead is off while the gateway learns the edge.
	var learn []time.Duration
	for i := 0; i < api.ServiceWakeAheadMinWakes+2+privateTCPWakeAheadMaxFailed; i++ {
		if d, ok := coldDial("learn", i); ok {
			learn = append(learn, d)
		}
	}

	optIn := func(enabled bool) {
		t.Helper()
		if raw, status := doReq(t, f.h, f.key, http.MethodPut, "/v1/apps/"+f.caller.Slug+"/service-wake-ahead", map[string]bool{"enabled": enabled}); status != http.StatusOK {
			t.Fatalf("set %s wake-ahead to %v: status=%d body=%s", f.caller.Slug, enabled, status, raw)
		}
		// The gateway caches the opt-in for ServiceWakeAheadCacheTTL.
		time.Sleep(api.ServiceWakeAheadCacheTTL + 5*time.Second)
	}
	var on, off []time.Duration
	for block := 0; block < privateTCPWakeAheadBlocks; block++ {
		enabled := block%2 == 0
		optIn(enabled)
		phase := map[bool]string{true: "ahead", false: "off"}[enabled]
		for i := 0; i < privateTCPWakeAheadBlockCycles; i++ {
			d, ok := coldDial(phase, block*privateTCPWakeAheadBlockCycles+i)
			switch {
			case !ok:
			case enabled:
				on = append(on, d)
			default:
				off = append(off, d)
			}
		}
	}
	onCycles := privateTCPWakeAheadBlocks / 2 * privateTCPWakeAheadBlockCycles

	caller, service := uuid.MustParse(f.caller.ID).String(), uuid.MustParse(f.service.ID).String()
	started := gatewayWakeAheadEdgeCount(t, f.h.GatewayControlURL, caller, service, "started")
	used := gatewayWakeAheadEdgeCount(t, f.h.GatewayControlURL, caller, service, "used")
	t.Logf("cold caller -> TCP service while learning: p50=%s p95=%s (n=%d)", percentileDuration(learn, 50), percentileDuration(learn, 95), len(learn))
	t.Logf("cold caller -> TCP service, wake-ahead off: p50=%s p95=%s (n=%d, interleaved)", percentileDuration(off, 50), percentileDuration(off, 95), len(off))
	t.Logf("cold caller -> TCP service, wake-ahead on:  p50=%s p95=%s (n=%d, interleaved)", percentileDuration(on, 50), percentileDuration(on, 95), len(on))
	t.Logf("wake-ahead of the TCP service for this caller: started=%g used=%g; failed cold dials: learn=%d off=%d ahead=%d", started, used, failed["learn"], failed["off"], failed["ahead"])
	if used < float64(onCycles)/2 {
		t.Fatalf("wake-ahead used %g of %d cold wakes (started %g); the TCP edge was not learned or not acted on", used, onCycles, started)
	}
}

// gatewayWakeAheadEdgeCount reads one gateway_service_wake_ahead_edge_total
// series from gatewayd-internal's metrics.
func gatewayWakeAheadEdgeCount(t *testing.T, controlURL, caller, target, outcome string) float64 {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(controlURL + "/metrics")
	if err != nil {
		t.Fatalf("scrape gateway metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		t.Fatalf("read gateway metrics: %v", err)
	}
	const name = "gateway_service_wake_ahead_edge_total{"
	want := map[string]string{"caller_app": caller, "target_app": target, "outcome": outcome}
	for _, line := range strings.Split(string(body), "\n") {
		rest, ok := strings.CutPrefix(line, name)
		if !ok {
			continue
		}
		labels, value, ok := strings.Cut(rest, "} ")
		if !ok || !labelsMatch(labels, want) {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return v
	}
	return 0
}

// labelsMatch reports whether a Prometheus label set (`a="x",b="y"`, values
// without commas) has exactly the wanted pairs.
func labelsMatch(labels string, want map[string]string) bool {
	pairs := strings.Split(labels, ",")
	if len(pairs) != len(want) {
		return false
	}
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || want[name] != strings.Trim(value, `"`) {
			return false
		}
	}
	return true
}

func percentileDuration(samples []time.Duration, p int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	return sorted[(len(sorted)-1)*p/100].Round(time.Millisecond)
}
