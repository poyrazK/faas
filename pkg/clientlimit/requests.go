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
	t := newTable(limits.PerSecond, limits.Burst, limits.TrackedAddrsMax, now)
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
