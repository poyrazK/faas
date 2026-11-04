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
// Only a host that has served an app's answer (servedByApp) gets a bucket of
// its own. Every other name shares one unknown-host bucket, so a flood of
// random names under the apps wildcard cannot fill the table, push a real
// host's bucket out and leave it on a shared overflow bucket the flood is
// draining.
func Hosts(next http.Handler, limits RequestLimits, host func(*http.Request) (string, bool), refused func(reason string), now func() time.Time) http.Handler {
	t := newTable[string](limits.PerSecond, limits.Burst, limits.TrackedAddrsMax, now)
	known := &knownHosts{set: map[string]struct{}{}, max: max(limits.TrackedAddrsMax, 1)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := host(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		isKnown := known.has(name)
		key := name
		if !isKnown {
			key = unknownHostKey
		}
		if allowed, remaining := t.take(key); !allowed {
			refused(ReasonHostRequestRate)
			w.Header().Set("Retry-After", "1")
			w.Header().Set("x-faas-rate-limit-scope", "host")
			api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
				"Rate limit exceeded", "this host is receiving more requests than this node serves for one host").
				WithLimit(int64(limits.Burst), int64(limits.Burst)-int64(remaining)).
				WithDocs(wire.DocsBaseURL+"/errors"))
			return
		}
		if isKnown {
			next.ServeHTTP(w, r)
			return
		}
		rec := &hostStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if servedByApp(rec.status()) && known.add(name) {
			// The request that proved the host is charged to its own
			// bucket too, so a known host's burst counts it.
			t.take(name)
		}
	})
}

// unknownHostKey is the shared bucket of hostnames that have not served an
// app's answer. A hostname cannot contain a NUL byte.
const unknownHostKey = "\x00unknown-host"

// servedByApp reports whether status is an answer only an app produces: a
// success, a protocol switch or a not-modified. A name that routes nowhere is
// answered 404, 421 or 503 by the edge, and a redirect may come from an alias
// domain before routing, so neither proves the host.
func servedByApp(status int) bool {
	return status >= 200 && status < 300 || status == http.StatusSwitchingProtocols || status == http.StatusNotModified
}

// knownHosts is the set of hostnames that served an app's answer. Only real
// hosts enter it; a full set is dropped whole.
type knownHosts struct {
	mu  sync.Mutex
	set map[string]struct{}
	max int
}

func (k *knownHosts) has(name string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.set[name]
	return ok
}

// add records name and reports whether it was new.
func (k *knownHosts) add(name string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.set[name]; ok {
		return false
	}
	if len(k.set) >= k.max {
		k.set = map[string]struct{}{}
	}
	k.set[name] = struct{}{}
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
