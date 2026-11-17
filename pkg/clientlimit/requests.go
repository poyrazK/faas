package clientlimit

import (
	"bufio"
	"net"
	"net/http"
	"net/netip"
	"sync"
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
// the hostnames tracked.
//
// The Host header is the client's to choose, so only a host that has served
// an app's answer (servedByApp) is tracked and limited. Any other name passes
// without taking a slot: random names under the apps wildcard can neither
// fill the table nor share and drain one bucket every tenant host depends on,
// and they cannot keep a real host untracked either, because its first
// answered request makes it known and limited. Each client stays bounded by
// the per-address limit in front of this one.
func Hosts(next http.Handler, limits RequestLimits, host func(*http.Request) (string, bool), refused func(reason string), now func() time.Time) http.Handler {
	return HostsWithKnown(next, limits, NewKnownHosts(limits.TrackedAddrsMax, now), host, refused, now)
}

// HostsWithKnown is Hosts recording the hosts that served an app's answer in
// known, so another layer can share the set (Shed).
func HostsWithKnown(next http.Handler, limits RequestLimits, known *KnownHosts, host func(*http.Request) (string, bool), refused func(reason string), now func() time.Time) http.Handler {
	if known == nil {
		known = NewKnownHosts(limits.TrackedAddrsMax, now)
	}
	t := newTable[string](limits.PerSecond, limits.Burst, limits.TrackedAddrsMax, now)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := host(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if !known.has(name) {
			rec := &hostStatusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if servedByApp(rec.status()) && known.add(name) {
				// The request that proved the host counts against its
				// bucket from the start.
				t.takeTracked(name)
			}
			return
		}
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
		next.ServeHTTP(w, r)
	})
}

// servedByApp reports whether status is an answer only an app produces: a
// success, a protocol switch or a not-modified. A name that routes nowhere is
// answered 404, 421 or 503 by the edge, and a redirect may come from an alias
// domain before routing, so neither proves the host.
func servedByApp(status int) bool {
	return status >= 200 && status < 300 || status == http.StatusSwitchingProtocols || status == http.StatusNotModified
}

// knownHostIdle is how long a known host may go unseen before a full set
// forgets it.
const knownHostIdle = 10 * time.Minute

// KnownHosts is the set of hostnames that served an app's answer, with when
// each was last seen. Only real hosts enter it. A full set forgets hosts idle
// past knownHostIdle; it never forgets a busy one, so a hot host cannot drop
// out of its limit.
type KnownHosts struct {
	mu  sync.Mutex
	set map[string]time.Time
	max int
	now func() time.Time
}

// NewKnownHosts returns an empty set of at most limit hosts.
func NewKnownHosts(limit int, now func() time.Time) *KnownHosts {
	if now == nil {
		now = time.Now
	}
	return &KnownHosts{set: map[string]time.Time{}, max: max(limit, 1), now: now}
}

func (k *KnownHosts) has(name string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.set[name]; !ok {
		return false
	}
	k.set[name] = k.now()
	return true
}

// add records name and reports whether it is newly known. A full set with no
// idle host to forget admits nothing: that host passes untracked, as with
// more real hosts than the set holds.
func (k *KnownHosts) add(name string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	if _, ok := k.set[name]; ok {
		k.set[name] = now
		return false
	}
	if len(k.set) >= k.max {
		for host, seen := range k.set {
			if now.Sub(seen) > knownHostIdle {
				delete(k.set, host)
			}
		}
		if len(k.set) >= k.max {
			return false
		}
	}
	k.set[name] = now
	return true
}

// hostStatusRecorder notes the status a response starts with. Unwrap keeps
// http.ResponseController's flush and hijack reaching the connection.
type hostStatusRecorder struct {
	http.ResponseWriter
	code     int
	hijacked bool
}

func (r *hostStatusRecorder) WriteHeader(code int) {
	if r.code == 0 || r.code < 200 && code >= 200 {
		r.code = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *hostStatusRecorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}

func (r *hostStatusRecorder) Flush() {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *hostStatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err == nil {
		r.hijacked = true
	}
	return conn, rw, err
}

func (r *hostStatusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *hostStatusRecorder) status() int {
	if r.hijacked {
		return http.StatusSwitchingProtocols
	}
	return r.code
}
