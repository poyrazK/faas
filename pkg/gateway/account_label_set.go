// Bounded admission for the gateway_per_account_rate_limited_total
// counter's account_id label (ADR-040 / issue #292, follow-up to
// the TODO at pkg/gateway/metrics.go:226-232).
//
// pkg/wire/metrics.go already implements accountLabelSet for the apid
// counter surface (issue #278). pkg/gateway has zero dependencies on
// pkg/wire today; importing pkg/wire solely to reuse the unexported
// primitive would couple the gateway package to the cross-daemon
// metrics graph (every daemon's OpsMetrics). The mirror below matches
// the wire-side semantics exactly so the two admit() implementations
// stay drop-in equivalent and a future shared utility package can
// collapse them without behaviour drift.
//
// Behavioural contract (mirrors pkg/wire/metrics.go:2546-2632):
//
//   - reserved labels ("anonymous", "__other__") are pre-admitted at
//     construction without consuming capacity;
//   - empty input normalises to "anonymous";
//   - real ids are admitted up to `cap - reservedCount`;
//   - overflow collapses to "__other__" without ever inserting into
//     the map (so it never resizes past cap);
//   - the map is non-evicting (deliberately a plain map+mutex, not
//     an LRU) — daemon restart is the only path that resets it.
//     An evicting LRU would let evicted ids re-admit later and grow
//     the Prometheus TSDB series set unbounded over the daemon's
//     lifetime (pkg/wire/metrics.go:2548-2554 documents the same
//     reasoning for the upstream primitive).
//
// Reserved values re-admitted on lookup. The Prometheus increment
// happens at the call site AFTER admit() returns so it is outside
// the critical section.
package gateway

import "sync"

// Reserved label values mirrored from pkg/wire/metrics.go.
// anonymousAccountLabel handles the empty-string and
// explicitly-anonymous cases; otherAccountLabel is the overflow
// placeholder (literal "__other__") that the §12 dashboard panel
// recognises.
const (
	anonymousAccountLabel = "anonymous"
	otherAccountLabel     = "__other__"
)

// accountLabelSetCap is the default real-id capacity. 10k mirrors
// pkg/wire/metrics.go's maxAccountLabelValues (issue #278). Tunable
// for tests via newAccountLabelSetWithCap below.
const accountLabelSetCap = 10_000

// accountLabelSet is the bounded admission set behind every
// account_id-labelled counter in this package's Metrics bundle.
// Reserved values are pre-admitted at construction; real ids
// consume capacity once and are never evicted in process.
//
// Pointer-receiver methods because the type contains a sync.Mutex —
// copying the value would duplicate the lock (govet copylocks).
// Constructed once per *Metrics in NewMetrics and held as a pointer
// field.
type accountLabelSet struct {
	mu       sync.Mutex
	admitted map[string]struct{}
	cap      int
}

// newAccountLabelSet constructs an admission set with the default
// production capacity (accountLabelSetCap = 10_000). Panics on
// non-positive capacity so a misconfigured daemon fails loud at
// boot rather than silently allowing unbounded admission.
func newAccountLabelSet() *accountLabelSet {
	return newAccountLabelSetWithCap(accountLabelSetCap)
}

// newAccountLabelSetWithCap is the test seam — capacity must be > 0;
// the call panics otherwise. Production goes through newAccountLabelSet;
// tests use a tiny capacity (e.g. 4) to verify overflow collapses to
// "__other__" in unit tests.
func newAccountLabelSetWithCap(capacity int) *accountLabelSet {
	if capacity <= 0 {
		panic("gateway: accountLabelSet capacity must be positive")
	}
	s := &accountLabelSet{
		admitted: make(map[string]struct{}, capacity+2), // +2 for the reserved labels
		cap:      capacity,
	}
	// Pre-admit reserved labels so admit() doesn't need a special branch.
	s.admitted[anonymousAccountLabel] = struct{}{}
	s.admitted[otherAccountLabel] = struct{}{}
	return s
}

// admit resolves an account id to its label value. Empty input
// normalises to anonymousAccountLabel. Reserved values
// (anonymousAccountLabel, otherAccountLabel) are always admitted
// without consuming capacity. Real ids are admitted up to capacity;
// further ids collapse to otherAccountLabel without ever consuming
// capacity, and the underlying map is never resized past cap.
//
// Concurrency: holds mu across the lookup+insert. Hot path is the
// "already admitted" lookup, which is O(1) and never inserts.
// The Prometheus increment at the call site happens AFTER admit
// returns, so it is outside the critical section.
func (s *accountLabelSet) admit(accountID string) string {
	switch accountID {
	case "":
		return anonymousAccountLabel
	case anonymousAccountLabel, otherAccountLabel:
		return accountID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[accountID]; ok {
		return accountID
	}
	// Reserved labels are pre-admitted at construction — they count
	// toward len(s.admitted) but not toward the user-facing capacity.
	// Subtract reservedCount so the real-id budget is exactly `cap -
	// reservedCount`, not `cap - reservedCount - 2`. Without the
	// subtraction the reserved labels steal two slots from the
	// real-id budget; the wire-side sibling's IP equivalent caught
	// this same flaw via TestFailedLoginTotal_OverflowCollapsesToOtherSlow
	// (issue #286, pkg/wire/metrics.go:2624-2625).
	const reservedCount = 2
	if len(s.admitted)-reservedCount >= s.cap {
		return otherAccountLabel
	}
	s.admitted[accountID] = struct{}{}
	return accountID
}
