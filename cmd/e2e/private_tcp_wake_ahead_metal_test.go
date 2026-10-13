//go:build metal

// adr: 1134 — wake-ahead learns private TCP edges. A caller that opens a TCP
// session to a parked internal service after each of its own cold wakes
// teaches the gateway that edge; once the caller opts in, the service is
// restored while the caller wakes instead of after it connects.
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

// privateTCPWakeAheadOnCycles is how many cold wakes are measured after the
// caller opts in.
const privateTCPWakeAheadOnCycles = 8

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
	coldDial := func(cycle string) time.Duration {
		t.Helper()
		f.park(t, f.service)
		f.park(t, f.caller)
		if wait := time.Until(lastWake.Add(api.ServiceWakeAheadFollowWindow + time.Second)); wait > 0 {
			time.Sleep(wait)
		}
		payload := cycle + "-" + randHexSuffix()
		lastWake = time.Now()
		got := f.dial(t, payload)
		elapsed := time.Since(lastWake)
		if !f.echoed(got, payload) {
			f.h.DumpLogs(t)
			t.Fatalf("%s: cold dial = %+v, want %q echoed", cycle, got, payload)
		}
		return elapsed
	}

	// Wake-ahead is off while the gateway learns the edge: these cycles are
	// the sequential baseline.
	var off []time.Duration
	for i := 0; i < api.ServiceWakeAheadMinWakes+2; i++ {
		off = append(off, coldDial("learn-"+strconv.Itoa(i)))
	}

	if raw, status := doReq(t, f.h, f.key, http.MethodPut, "/v1/apps/"+f.caller.Slug+"/service-wake-ahead", map[string]bool{"enabled": true}); status != http.StatusOK {
		t.Fatalf("opt %s into wake-ahead: status=%d body=%s", f.caller.Slug, status, raw)
	}
	// The gateway caches the opt-in for ServiceWakeAheadCacheTTL.
	time.Sleep(api.ServiceWakeAheadCacheTTL + 5*time.Second)

	var on []time.Duration
	for i := 0; i < privateTCPWakeAheadOnCycles; i++ {
		on = append(on, coldDial("ahead-"+strconv.Itoa(i)))
	}

	caller, service := uuid.MustParse(f.caller.ID).String(), uuid.MustParse(f.service.ID).String()
	started := gatewayWakeAheadEdgeCount(t, f.h.GatewayControlURL, caller, service, "started")
	used := gatewayWakeAheadEdgeCount(t, f.h.GatewayControlURL, caller, service, "used")
	t.Logf("cold caller -> TCP service, wake-ahead off: p50=%s p95=%s (n=%d)", percentileDuration(off, 50), percentileDuration(off, 95), len(off))
	t.Logf("cold caller -> TCP service, wake-ahead on:  p50=%s p95=%s (n=%d)", percentileDuration(on, 50), percentileDuration(on, 95), len(on))
	t.Logf("wake-ahead of the TCP service for this caller: started=%g used=%g", started, used)
	if used < privateTCPWakeAheadOnCycles/2 {
		t.Fatalf("wake-ahead used %g of %d cold wakes (started %g); the TCP edge was not learned or not acted on", used, privateTCPWakeAheadOnCycles, started)
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
