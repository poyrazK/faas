// Bounded admission for the gateway_tls_cert_expiry_by_host_seconds
// gauge's hostname label (Finding 2 / ADR-024 H3 follow-up).
//
// The aggregate gateway_tls_cert_expiry_seconds gauge collapses every
// cached cert into one number — operators can't tell WHICH host is
// about to expire, only THAT some host is. Per-host visibility needs
// the per-host series, but the host label is unbounded in principle
// (every customer custom domain adds one). This primitive mirrors
// accountLabelSet (pkg/gateway/account_label_set.go, itself mirroring
// pkg/wire/metrics.go:2546-2632) so the admission contract is
// drop-in equivalent.
//
// Capacity is intentionally LOWER than the account set (10k vs 10k):
// certs are denser per tenant and stale host labels accumulate until
// process restart because the set is non-evicting. A future eviction
// follow-up may add LRU semantics when domain churn justifies it; for
// now the cap is the daemon-lifetime cardinality ceiling.
//
// Reserved labels (anonymous, __other__) carry the same meaning as
// the account set: anonymous handles the empty-string input (no
// host name observed); __other__ is the overflow placeholder that
// the §12 dashboard panel and the alert rules recognise.
//
// Behavioural contract (mirrors accountLabelSet / pkg/wire):
//   - reserved labels are pre-admitted at construction;
//   - empty input normalises to anonymous;
//   - real hostnames are admitted up to `cap - reservedCount`;
//   - overflow collapses to "__other__" without ever consuming capacity;
//   - non-evicting; daemon restart is the only reset path.
package gateway

import "sync"

// Reserved hostname-label values.
const (
	anonymousHostnameLabel = "anonymous"
	otherHostnameLabel     = "__other__"
)

// hostnameLabelSetCap is the default real-hostname capacity. 10k is
// a deliberate cap-and-document choice — see the package doc above
// for the rationale (denser per tenant than accounts; non-evicting).
const hostnameLabelSetCap = 10_000

// hostnameLabelSet is the bounded admission set for the
// `hostname` label on gateway_tls_cert_expiry_by_host_seconds.
// Reserved values are pre-admitted at construction; real
// hostnames consume capacity once and are never evicted in process.
//
// Pointer-receiver methods because the type contains a sync.Mutex —
// copying the value would duplicate the lock (govet copylocks).
// Constructed once per *Metrics in NewMetrics and held as a pointer
// field.
type hostnameLabelSet struct {
	mu       sync.Mutex
	admitted map[string]struct{}
	cap      int
}

// newHostnameLabelSet constructs an admission set with the default
// production capacity (hostnameLabelSetCap = 10_000). Panics on
// non-positive capacity so a misconfigured daemon fails loud at boot.
func newHostnameLabelSet() *hostnameLabelSet {
	return newHostnameLabelSetWithCap(hostnameLabelSetCap)
}

// newHostnameLabelSetWithCap is the test seam — capacity must be > 0;
// the call panics otherwise. Production goes through
// newHostnameLabelSet; tests use a tiny capacity (e.g. 4) to verify
// overflow collapses to "__other__" in unit tests.
func newHostnameLabelSetWithCap(capacity int) *hostnameLabelSet {
	if capacity <= 0 {
		panic("gateway: hostnameLabelSet capacity must be positive")
	}
	s := &hostnameLabelSet{
		admitted: make(map[string]struct{}, capacity+2),
		cap:      capacity,
	}
	s.admitted[anonymousHostnameLabel] = struct{}{}
	s.admitted[otherHostnameLabel] = struct{}{}
	return s
}

// admit resolves a hostname to its label value. Empty input
// normalises to anonymousHostnameLabel. Reserved values
// (anonymousHostnameLabel, otherHostnameLabel) are always admitted
// without consuming capacity. Real hostnames are admitted up to
// capacity; further hostnames collapse to otherHostnameLabel
// without ever consuming capacity.
//
// Concurrency: holds mu across the lookup+insert. Hot path is the
// "already admitted" lookup, which is O(1) and never inserts.
// The Prometheus write at the call site happens AFTER admit
// returns, so it is outside the critical section.
func (s *hostnameLabelSet) admit(hostname string) string {
	switch hostname {
	case "":
		return anonymousHostnameLabel
	case anonymousHostnameLabel, otherHostnameLabel:
		return hostname
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admitted[hostname]; ok {
		return hostname
	}
	// Reserved labels are pre-admitted at construction — subtract
	// reservedCount so the real-hostname budget is exactly `cap -
	// reservedCount`. Mirrors the same correction in
	// pkg/wire/metrics.go:2624-2625 (issue #286 fix).
	const reservedCount = 2
	if len(s.admitted)-reservedCount >= s.cap {
		return otherHostnameLabel
	}
	s.admitted[hostname] = struct{}{}
	return hostname
}

// snapshot returns a copy of the currently-admitted hostname set.
// Used by the cert-expiry refresher to delete stale host gauges:
// after a walk, any hostname in `previous` that is NOT in the new
// walk's snapshot must be deleted so Prometheus drops the series.
// Caller does NOT hold the mutex.
func (s *hostnameLabelSet) snapshot() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]struct{}, len(s.admitted))
	for k := range s.admitted {
		out[k] = struct{}{}
	}
	return out
}
