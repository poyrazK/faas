// Bounded admission for the per-app route label on the
// opt-in `gateway_request_duration_seconds{app, route, class}`
// histogram (ADR-093 / issue #273).
//
// The route label is method + raw path (pre-ADR-091 edge-rule
// rewrite). It is attacker-influenced under wildcard path
// patterns (`/users/{uuid}` produces unbounded distinct labels),
// so every app's routeLabelSet gets a hard cap of 50 distinct
// real routes — overflow collapses to the reserved
// `__route_other__` bucket without ever inserting into the map.
//
// This is the in-package mirror of pkg/gateway/account_label_set.go
// (ADR-040) and pkg/gateway/hostname_label_set.go (ADR-024 §4).
// pkg/gateway cannot import pkg/wire (cycle, documented at
// cmd/gatewayd-internal/topn.go:14-21), so each label-set family
// has its own copy with the same contract. The same cap-vs-bucket
// reasoning applies: the per-app cap is the only thing standing
// between us and `O(paths)` series on the Prometheus side.
//
// Behavioural contract (mirrors account_label_set.go):
//
//   - reserved labels ("", "__route_other__") are pre-admitted at
//     construction without consuming capacity;
//   - empty input normalises to "" (the gateway-side sentinel for
//     "no appID resolved" — same value the existing
//     `gateway_requests_total{app="-"}` series uses);
//   - real routes are admitted up to `cap - reservedCount`;
//   - overflow collapses to "__route_other__" without ever
//     inserting into the map (so it never resizes past cap);
//   - the map is not an LRU. A full set only gives up a label that
//     has gone api.RouteLabelReuseAfterIdle without a request, and
//     never a declared route (ADR-20261026143210 amendment 1): an
//     app that answers every path would otherwise keep the first 50
//     probed paths for the daemon's lifetime and label every real
//     route __route_other__. The evicted label's series are deleted
//     with it (onEvict), so live series stay within the cap and the
//     gateway's series budget; churn is bounded by one replacement
//     per label per idle window.
//
// The Prometheus increment happens at the call site AFTER admit()
// returns so it is outside the critical section.
package gateway

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// reservedRouteLabel is the empty-string sentinel used by the
// pre-existing `gateway_requests_total{app="-"}` series when the
// request didn't resolve to an app (handler.go:2759-2761). The
// empty route label rides the same path — it is the "no route
// label was computed" state.
const reservedRouteLabelEmpty = ""

// otherRouteLabel is the overflow bucket (literal "__route_other__")
// recognised by the ADR-093 dashboard panel and the
// `GatewayWildcardRoute` Prometheus alert. The ADR-091 edge-rule
// vocabulary reserves the `__` prefix for engine-internal
// placeholders, so "__route_other__" cannot collide with a
// customer-supplied route label.
const (
	otherRouteLabel = "__route_other__"
)

// routeLabelSetCap is the default per-app real-route capacity.
// 50 mirrors pkg/api/RouteMetricsPerAppCap (ADR-093 D2). The two
// constants are the same number on purpose: the cap is the load-
// bearing number that bounds the per-app Prometheus series set,
// and tying it to pkg/api lets the operator-level kill-switch
// (cmd/gatewayd-internal/config.go) reason about
// "what does 50 mean?" without a separate comment.
//
// Tunable for tests via newRouteLabelSetWithCap below.
const routeLabelSetCap = 50

// routeLabelSet is the bounded admission set behind every
// per-app, per-route metric in this package's Metrics bundle.
// Reserved values are pre-admitted at construction; real routes
// consume capacity once and are never evicted in process.
//
// Pointer-receiver methods because the type contains a sync.Mutex —
// copying the value would duplicate the lock (govet copylocks).
// Constructed once per app by Handler after Backend.Lookup
// resolves the app and the App.RouteMetricsEnabled flag is true;
// held as a Handler field keyed by appID so the map outlives any
// single request.
type routeLabelSet struct {
	mu       sync.Mutex
	admitted map[string]routeLabelEntry
	cap      int
	// now and reuseAfter decide when an idle label may be replaced;
	// onEvict (nil-safe) deletes a replaced label's series.
	now        func() time.Time
	reuseAfter time.Duration
	onEvict    func(route string)
}

// routeLabelEntry is one admitted label: when a request last carried it
// and whether it is a declared route, which is never replaced.
type routeLabelEntry struct {
	lastSeen int64 // unix nanoseconds
	declared bool
}

// newRouteLabelSet constructs an admission set with the default
// per-app capacity (routeLabelSetCap = 50). Panics on non-positive
// capacity so a misconfigured daemon fails loud at boot rather
// than silently allowing unbounded admission.
func newRouteLabelSet() *routeLabelSet {
	return newRouteLabelSetWithCap(routeLabelSetCap)
}

// newRouteLabelSetWithCap is the test seam — capacity must be > 0;
// the call panics otherwise. Production goes through
// newRouteLabelSet; tests use a tiny capacity (e.g. 4) to verify
// overflow collapses to "__route_other__" in unit tests.
func newRouteLabelSetWithCap(capacity int) *routeLabelSet {
	if capacity <= 0 {
		panic("gateway: routeLabelSet capacity must be positive")
	}
	s := &routeLabelSet{
		admitted:   make(map[string]routeLabelEntry, capacity+2), // +2 for the reserved labels
		cap:        capacity,
		now:        time.Now,
		reuseAfter: api.RouteLabelReuseAfterIdle,
	}
	// Pre-admit reserved labels so admit() doesn't need a special branch.
	// They are declared so a full set never offers them for replacement.
	s.admitted[reservedRouteLabelEmpty] = routeLabelEntry{declared: true}
	s.admitted[otherRouteLabel] = routeLabelEntry{declared: true}
	return s
}

// admit resolves a route identifier to its label value. Empty
// input normalises to reservedRouteLabelEmpty — the same empty
// string the existing `gateway_requests_total{app="-"}` series
// uses when the appID didn't resolve. Reserved values
// (reservedRouteLabelEmpty, otherRouteLabel) are always admitted
// without consuming capacity. Real routes are admitted up to
// capacity; further routes collapse to otherRouteLabel without
// ever consuming capacity, and the underlying map is never resized
// past cap.
//
// Concurrency: holds mu across the lookup+insert. Hot path is the
// "already admitted" lookup, which is O(1) and never inserts.
// The Prometheus increment at the call site happens AFTER admit
// returns, so it is outside the critical section.
func (s *routeLabelSet) admit(route string) string {
	return s.admitEntry(route, false)
}

// admitDeclared admits a declared route (OpenAPI, gregale.yaml), which a
// full set never replaces.
func (s *routeLabelSet) admitDeclared(route string) string {
	return s.admitEntry(route, true)
}

func (s *routeLabelSet) admitEntry(route string, declared bool) string {
	// Reserved values (empty + otherRouteLabel) are always admitted
	// without consuming capacity. The empty case is folded into the
	// multi-value branch because reservedRouteLabelEmpty == "" —
	// distinct case clauses that label the same string trigger the
	// compiler's duplicate-case check (we hit it once already).
	switch route {
	case reservedRouteLabelEmpty, otherRouteLabel:
		return route
	}
	s.mu.Lock()
	now := s.now().UnixNano()
	if entry, ok := s.admitted[route]; ok {
		entry.lastSeen, entry.declared = now, entry.declared || declared
		s.admitted[route] = entry
		s.mu.Unlock()
		return route
	}
	// Reserved labels are pre-admitted at construction — they count
	// toward len(s.admitted) but not toward the user-facing capacity.
	// Subtract reservedCount so the real-id budget is exactly `cap -
	// reservedCount`, not `cap - reservedCount - 2`. Without the
	// subtraction the reserved labels steal two slots from the
	// real-route budget; the IP equivalent at
	// account_label_set.go:122-123 caught this same flaw via
	// TestFailedLoginTotal_OverflowCollapsesToOtherSlow.
	const reservedCount = 2
	evicted := ""
	if len(s.admitted)-reservedCount >= s.cap {
		evicted = s.idlestLocked(now)
		if evicted == "" {
			s.mu.Unlock()
			return otherRouteLabel
		}
		delete(s.admitted, evicted)
	}
	s.admitted[route] = routeLabelEntry{lastSeen: now, declared: declared}
	onEvict := s.onEvict
	s.mu.Unlock()
	// Series deletion takes the metric vectors' locks; keep it outside mu.
	if evicted != "" && onEvict != nil {
		onEvict(evicted)
	}
	return route
}

// idlestLocked returns the least recently seen undeclared label that has
// been idle for reuseAfter, or "" when every label is live or declared.
// Caller holds mu; the scan is bounded by the set's capacity.
func (s *routeLabelSet) idlestLocked(now int64) string {
	cutoff := now - int64(s.reuseAfter)
	idlest, oldest := "", int64(0)
	for route, entry := range s.admitted {
		if entry.declared || entry.lastSeen > cutoff {
			continue
		}
		if idlest == "" || entry.lastSeen < oldest || entry.lastSeen == oldest && route < idlest {
			idlest, oldest = route, entry.lastSeen
		}
	}
	return idlest
}

// AdmitForTest is the test-only seam that inserts a route
// without going through the cap admission check. Production
// code MUST NOT call this — only tests via the build-tag-free
// pkg/gateway test files. Exists so a unit test can seed a
// known state without the cap-vs-overflow dance.
func (s *routeLabelSet) AdmitForTest(route string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admitted[route] = routeLabelEntry{lastSeen: s.now().UnixNano()}
}
