package clientlimit

import (
	"net/http"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// RequestLimits bound one client address's request rate.
type RequestLimits struct {
	// PerSecond and Burst are the token bucket for requests.
	PerSecond float64
	Burst     int
	// TrackedAddrsMax bounds the addresses kept in memory.
	TrackedAddrsMax int
}

// Requests answers 429 to a client address that sends requests faster than
// its bucket allows. clientAddr names the request's client; a request it
// cannot name passes. refused is called for each refusal.
func Requests(next http.Handler, limits RequestLimits, clientAddr func(*http.Request) (netip.Addr, bool), refused func(reason string), now func() time.Time) http.Handler {
	t := newTable[netip.Prefix](limits.PerSecond, limits.Burst, limits.TrackedAddrsMax, now)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if addr, ok := clientAddr(r); ok {
			if allowed, remaining := t.take(Key(addr)); !allowed {
				refused(ReasonRequestRate)
				w.Header().Set("Retry-After", "1")
				w.Header().Set("x-faas-rate-limit-scope", "client-address")
				api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
					"Rate limit exceeded", "this address has sent too many requests to this node").
					WithLimit(int64(limits.Burst), int64(limits.Burst)-int64(remaining)).
					WithDocs(wire.DocsBaseURL+"/errors"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Hosts answers 429 to a hostname that receives requests faster than its
// bucket allows, on this node and before the request is routed or wakes
// anything: a flood on one app reaches every node that takes its host, and
// this keeps that app's neighbours served. host names the request's
// hostname; a request it cannot name passes. limits.TrackedAddrsMax bounds
// the hostnames tracked. The Host header is the client's to choose, so a
// hostname the full table cannot track passes rather than share one bucket:
// a flood of random hostnames must not refuse every tenant's host. Each
// client stays bounded by the per-address limit in front of this one.
func Hosts(next http.Handler, limits RequestLimits, host func(*http.Request) (string, bool), refused func(reason string), now func() time.Time) http.Handler {
	t := newTable[string](limits.PerSecond, limits.Burst, limits.TrackedAddrsMax, now)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := host(r); ok {
			if allowed, remaining := t.takeTracked(name); !allowed {
				refused(ReasonHostRequestRate)
				w.Header().Set("Retry-After", "1")
				w.Header().Set("x-faas-rate-limit-scope", "host")
				api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
					"Rate limit exceeded", "this host is receiving more requests than this node serves for one host").
					WithLimit(int64(limits.Burst), int64(limits.Burst)-int64(remaining)).
					WithDocs(wire.DocsBaseURL+"/errors"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
