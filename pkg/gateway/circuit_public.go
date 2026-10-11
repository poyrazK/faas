package gateway

// Public-path instance breaker — ADR-201 §2, H4-68.
//
// The service proxy has run the shared instance-health breaker since
// ADR-201, but the public picker never consulted it and nothing applied a
// customer's kind=circuit_breaker rule. With FAAS_GATEWAY_CIRCUIT_BREAKER on,
// the public path now shares the service proxy's group: a transport failure
// on either path counts against the instance, an instance whose circuit is
// open is skipped by the picker, and when every candidate is open the request
// fails fast instead of waiting on a known-dead target.

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
)

// circuitMaxRepicks bounds the sibling search when the picked instance's
// circuit is open.
const circuitMaxRepicks = 4

// circuitOpenRetryAfterSeconds is the Retry-After on a circuit_open answer:
// the default first open interval, after which a probe is admitted.
const circuitOpenRetryAfterSeconds = "5"

// WithCircuitBreaker installs the instance-health breaker group shared with
// the service proxy. Nil (the FAAS_GATEWAY_CIRCUIT_BREAKER-off default)
// leaves picking and forwarding unchanged.
func (h *Handler) WithCircuitBreaker(g *circuit.Group) *Handler {
	h.breaker = g
	return h
}

func circuitKey(appID, instanceID string) string { return serviceProxyEndpointKey(appID, instanceID) }

// selectByCircuit returns pick when its instance may receive traffic, else
// the first sibling repick returns that may. probe reports that the
// admission reserved the instance's half-open probe, which the caller must
// release if the request never reaches the target.
func (h *Handler) selectByCircuit(appID string, pick PickResult, repick func() PickResult) (out PickResult, ok, probe bool) {
	if h.breaker == nil || !pick.OK {
		return pick, true, false
	}
	h.pruneIdleCircuits()
	seen := make(map[string]bool, circuitMaxRepicks+1)
	for i := 0; ; i++ {
		if id := pick.Target.InstanceID; !seen[id] {
			seen[id] = true
			if allowed, reserved := h.breaker.Admit(circuitKey(appID, id)); allowed {
				return pick, true, reserved
			}
		}
		if i == circuitMaxRepicks {
			return pick, false, false
		}
		next := repick()
		if !next.OK {
			return pick, false, false
		}
		pick = next
	}
}

// pruneIdleCircuits drops breaker keys for instances no request has touched
// within serviceProxyIdleRetention, at most once per sweep interval. The
// service proxy prunes on its own path; an app with only public traffic
// would otherwise keep one key per instance it ever served.
func (h *Handler) pruneIdleCircuits() {
	now := time.Now().UnixNano()
	last := h.circuitLastSweep.Load()
	if last != 0 && time.Duration(now-last) < serviceProxySweepInterval {
		return
	}
	if h.circuitLastSweep.CompareAndSwap(last, now) {
		h.breaker.PruneIdle(serviceProxyIdleRetention)
	}
}

// releaseCircuitProbe gives back a half-open probe this request reserved but
// never settled (it ended before an outcome was observed).
func (h *Handler) releaseCircuitProbe(appID, instanceID string) {
	if h.breaker != nil {
		h.breaker.Release(circuitKey(appID, instanceID))
	}
}

// recordCircuitFailure counts a transport failure against an instance.
func (h *Handler) recordCircuitFailure(appID, instanceID string) {
	if h.breaker != nil {
		h.breaker.Failure(circuitKey(appID, instanceID))
	}
}

// circuitObserved reports each forward attempt's outcome to the breaker. A
// transport failure is recorded by the stale-target retirement as soon as it
// is detected; any answer from the guest, whatever its status, proves the
// instance reachable; a request that ended without either teaches nothing.
func (h *Handler) circuitObserved(appID string, forward retryAttempt) retryAttempt {
	if h.breaker == nil {
		return forward
	}
	return func(w http.ResponseWriter, r *http.Request, target Target) {
		forward(w, r, target)
		key := circuitKey(appID, target.InstanceID)
		switch {
		case staleTargetMarked(r.Context()):
		case r.Context().Err() != nil:
			h.breaker.Release(key)
		default:
			h.breaker.Success(key)
		}
	}
}

// writeCircuitOpen answers a request whose every candidate instance has an
// open circuit.
func writeCircuitOpen(w http.ResponseWriter) {
	w.Header().Set("Retry-After", circuitOpenRetryAfterSeconds)
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCircuitOpen, "Circuit open",
		"every instance of this app recently failed at the transport layer; the platform is holding traffic while one recovering instance is probed"))
}
