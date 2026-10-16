// adr: 1134
package gateway

import (
	"sync"
	"testing"
)

type tcpWakeAheadObservation struct {
	caller, target string
	woken          bool
	hopsBefore     int // guest dials already made when the learner saw it
}

// observeTCPWakeAhead records what the service proxy hands the ADR-977
// learner for TCP sessions.
func observeTCPWakeAhead(h *tcpTestHarness) func() []tcpWakeAheadObservation {
	var mu sync.Mutex
	var seen []tcpWakeAheadObservation
	h.proxy.cfg.Services.observeCall = func(caller, target string, woken bool) {
		h.mu.Lock()
		hops := len(h.hops)
		h.mu.Unlock()
		mu.Lock()
		seen = append(seen, tcpWakeAheadObservation{caller: caller, target: target, woken: woken, hopsBefore: hops})
		mu.Unlock()
	}
	return func() []tcpWakeAheadObservation {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		return out
	}
}

// ADR-1134: a private TCP session teaches wake-ahead like an HTTP call. It is
// observed once, when it starts (before the guest dial), with whether it had
// to wake the target.
func TestServiceTCPProxyFeedsWakeAheadLearner(t *testing.T) {
	h := newTCPTestHarness(t)
	observed := observeTCPWakeAhead(h)
	h.onWake = func() {
		h.provider.set("db",
			ServiceEndpoint{InstanceID: "i1", NodeID: "n1", Port: 8080},
			ServiceEndpoint{InstanceID: "i2", NodeID: "n2", Port: 8080},
		)
	}

	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "" {
		t.Fatalf("parked target: reason=%q", reason)
	}
	if got := observed(); len(got) != 1 || got[0] != (tcpWakeAheadObservation{caller: "api", target: "db", woken: true}) {
		t.Fatalf("parked target: observations = %+v, want one woken api→db before any guest dial", got)
	}

	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "" {
		t.Fatalf("warm target: reason=%q", reason)
	}
	if got := observed(); len(got) != 1 || got[0].woken || got[0].hopsBefore != 1 {
		t.Fatalf("warm target: observations = %+v, want one warm api→db observed before its own dial", got)
	}

	// A replica that cannot be dialed is retried on another; the session is
	// still one observation.
	h.dead["i1"], h.dead["i2"] = true, false
	h.proxy.cfg.Services.next["db"] = 0
	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "" {
		t.Fatalf("retried session: reason=%q", reason)
	}
	if got := observed(); len(got) != 1 {
		t.Fatalf("retried session: observations = %+v, want exactly one", got)
	}
}

func TestServiceTCPProxyDoesNotTeachWakeAheadFromRefusalsOrPreviews(t *testing.T) {
	h := newTCPTestHarness(t)
	observed := observeTCPWakeAhead(h)
	h.provider.set("db", ServiceEndpoint{InstanceID: "i1", NodeID: "n1", Port: 8080})

	if _, reason := h.serve(t, "caller", "198.19.0.7:22"); reason != "undeclared_port" {
		t.Fatalf("undeclared port: reason=%q", reason)
	}
	h.preview = "api"
	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "" {
		t.Fatalf("preview caller: reason=%q", reason)
	}
	if got := observed(); len(got) != 0 {
		t.Fatalf("observations = %+v, want none for a refused session or a PR preview caller", got)
	}
}
