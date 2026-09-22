package gateway

// Stale route tier (ADR-190, decision 3).
//
// PGBackend.Lookup is cache-first: RouteCache holds host → app_id and
// a miss is one Postgres lookup through the Router. When that lookup
// returns an error the request 404s. Any route that has been
// invalidated by an app_routes_changed notify, evicted by the 10k LRU
// cap, or never seen since the last gateway restart is therefore
// unreachable for the entire duration of a Postgres outage — a
// control-plane failure becomes a data-plane failure for exactly the
// apps whose routes changed recently.
//
// This tier keeps a bounded, TTL-limited copy of the last App each
// host resolved to. It is consulted only when the Router errors,
// never when the Router says the host does not exist, so a deleted
// custom domain stops routing as soon as Postgres answers. The TTL
// bounds how long a removed route can be served while Postgres is
// unreachable; the ADR records that trade-off.

import (
	"os"
	"sync"
	"time"
)

// RouteStaleTTLEnv overrides how long a last-known-good route may be
// served after its last successful resolution while the Router is
// failing. Go duration; unset or invalid falls back to
// defaultRouteStaleTTL. "0" disables the tier.
const RouteStaleTTLEnv = "FAAS_GATEWAY_ROUTE_STALE_TTL"

const defaultRouteStaleTTL = 10 * time.Minute

// staleLogEvery bounds the "serving stale route" warning to once per
// host per interval so an outage does not turn every request into a
// log line.
const staleLogEvery = time.Minute

func routeStaleTTL() time.Duration {
	raw := os.Getenv(RouteStaleTTLEnv)
	if raw == "" {
		return defaultRouteStaleTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return defaultRouteStaleTTL
	}
	return d
}

type staleRouteEntry struct {
	app      App
	resolved time.Time
	lastLog  time.Time
}

// staleRoutes is the last-known-good host → App map. Bounded by cap
// with FIFO eviction on insert (a route that resolved earliest is the
// least valuable to keep), expired by ttl on read.
type staleRoutes struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	now   func() time.Time
	byKey map[string]*staleRouteEntry
	order []string // insertion order for FIFO eviction
}

func newStaleRoutes(capacity int, ttl time.Duration) *staleRoutes {
	if capacity < 1 {
		capacity = 1
	}
	return &staleRoutes{cap: capacity, ttl: ttl, now: time.Now, byKey: map[string]*staleRouteEntry{}}
}

// Put records a successful resolution. A disabled tier (ttl <= 0)
// stores nothing.
func (s *staleRoutes) Put(host string, app App) {
	if s == nil || s.ttl <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.byKey[host]; ok {
		e.app = app
		e.resolved = s.now()
		return
	}
	for len(s.byKey) >= s.cap && len(s.order) > 0 {
		victim := s.order[0]
		s.order = s.order[1:]
		delete(s.byKey, victim)
	}
	s.byKey[host] = &staleRouteEntry{app: app, resolved: s.now()}
	s.order = append(s.order, host)
}

// Get returns the last-known-good App for host if it is within ttl.
// shouldLog is true at most once per staleLogEvery per host so the
// caller can warn without flooding.
func (s *staleRoutes) Get(host string) (app App, ok bool, shouldLog bool) {
	if s == nil || s.ttl <= 0 {
		return App{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.byKey[host]
	if !found {
		return App{}, false, false
	}
	now := s.now()
	if now.Sub(e.resolved) > s.ttl {
		delete(s.byKey, host)
		s.removeFromOrder(host)
		return App{}, false, false
	}
	if now.Sub(e.lastLog) >= staleLogEvery {
		e.lastLog = now
		shouldLog = true
	}
	return e.app, true, shouldLog
}

// Delete forgets host. Called when the Router positively reports the
// host no longer routes, so a removed domain is never served stale
// once Postgres has answered.
func (s *staleRoutes) Delete(host string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byKey[host]; !ok {
		return
	}
	delete(s.byKey, host)
	s.removeFromOrder(host)
}

// Len reports the number of retained entries (tests and metrics).
func (s *staleRoutes) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byKey)
}

func (s *staleRoutes) removeFromOrder(host string) {
	for i, h := range s.order {
		if h == host {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}
