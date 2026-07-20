// On-demand HTTP-01 allowlist (spec §11, §7). CertMagic's
// OnDemand.DecisionFunc asks "may I mint a cert for this hostname?"; we
// answer by looking up the customer-verified custom_domains row.
//
// Why this lives in pkg/gateway (not pkg/state): the allowlist is part of the
// edge's TLS seam. pkg/state holds the rows; pkg/gateway decides what to do
// with them. The query is identical to the one pgRouter.ResolveHost uses for
// routing (cmd/gatewayd/backend.go), so we cannot serve one hostname from
// routing and a different one from the allowlist — they share the Store.
//
// Caching: none today. The custom_domains table is small (one per customer
// domain, ~10⁴ at fleet scale), the query is index-keyed, and certmagic
// serializes on-demand mints per hostname via an in-process mutex. Add a
// short TTL cache here if the table grows past that.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// OnDemandLookup is the function signature NewPGAllowlist consumes. It is a
// function (not an interface on a Store) so callers don't have to declare an
// adapter type that bridges the (state.CustomDomain) → (any) return-type
// mismatch. Production passes a closure that calls state.PgStore.DomainByName
// and wraps state.ErrNotFound as gateway.ErrNotFound; tests inject fakes
// directly. The function returns any because the result is type-asserted on
// the Verified() method below — pkg/gateway stays free of pkg/state.
type OnDemandLookup func(ctx context.Context, domain string) (any, error)

// verified is the shape NewPGAllowlist needs from the lookup result. The
// concrete state.CustomDomain satisfies it; tests use fakeCustomDomain.
// Keeping this as a local interface (rather than importing pkg/state)
// means pkg/gateway stays free of pgx.
type verified interface {
	Verified() bool
}

// ErrNotFound is the sentinel NewPGAllowlist recognizes as "this hostname is
// not in the custom_domains table" so it can return false without logging at
// Warn level (the steady-state denial path; logging here would flood the
// gatewayd log on every scan of an unowned hostname). Callers MUST surface
// this sentinel from their lookup closure when the row is missing — wrapping
// state.ErrNotFound (or any other concrete store sentinel) is the production
// path in cmd/gatewayd.
var ErrNotFound = errors.New("gateway: domain not found in allowlist")

// NewPGAllowlist returns an OnDemandAllowlist backed by store. The store must
// be the same instance pgRouter uses for routing so the two can't drift (a
// hostname that routes must be allowlisted, and vice versa). The slog logger
// is used to record denied on-demand requests — those are the loud signal
// that someone is poking the edge for a hostname we don't own.
//
// NewPGAllowlist never panics on store==nil: a nil lookup is treated as
// deny-all, which is the safe fail-closed default for an unconfigured edge.
func NewPGAllowlist(store OnDemandLookup, log *slog.Logger) OnDemandAllowlist {
	if log == nil {
		log = slog.Default()
	}
	return func(host string) bool {
		if store == nil {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		raw, err := store(ctx, host)
		if err != nil {
			// NotFound is the steady-state denial path (someone probed a
			// hostname we don't own); everything else is a DB problem we
			// want to surface. Callers MUST surface ErrNotFound from the
			// lookup layer so this branch fires correctly.
			if errors.Is(err, ErrNotFound) {
				return false
			}
			log.Warn("gateway: allowlist lookup failed; failing closed",
				"host", host, "err", err)
			return false
		}
		v, ok := raw.(verified)
		if !ok {
			// The concrete store returned a value that doesn't expose
			// Verified() — that's a contract violation by the store, and the
			// safe default is to deny.
			log.Warn("gateway: allowlist lookup returned non-verified type; failing closed",
				"host", host)
			return false
		}
		if !v.Verified() {
			log.Info("gateway: on-demand denied: domain exists but TXT challenge unverified",
				"host", host)
			return false
		}
		return true
	}
}

// StaticAllowlist returns an OnDemandAllowlist that allows exactly the given
// hostnames. Used by tests and by the staging path where CertMagic's
// staging-CA allows a fixed hostname.
func StaticAllowlist(hosts ...string) OnDemandAllowlist {
	set := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		set[h] = struct{}{}
	}
	return func(host string) bool {
		_, ok := set[host]
		return ok
	}
}

// CountingAllowlist wraps an inner allowlist and records how many times it
// returned true/false. Used by tests to assert the CertMagic wire consults
// the callback (instead of bypassing it via the wildcard cert cache).
//
// Counter access goes through atomic.Int64 so tests can read it concurrently
// with the callback (CertMagic invokes the decision func on its own goroutine).
type CountingAllowlist struct {
	Inner OnDemandAllowlist
	Allow atomic.Int64
	Deny  atomic.Int64
	mu    sync.Mutex
	seen  []string
}

// NewCountingAllowlist wraps inner. If inner is nil, StaticAllowlist() (deny-all)
// is used so a nil-wrapped counter still records calls.
func NewCountingAllowlist(inner OnDemandAllowlist) *CountingAllowlist {
	if inner == nil {
		inner = StaticAllowlist()
	}
	return &CountingAllowlist{Inner: inner}
}

// Allow returns true iff inner does, and increments the matching counter.
// The signature matches OnDemandAllowlist via a tiny wrapper below; using a
// method keeps the inner field unexported-safe.
func (c *CountingAllowlist) allow(host string) bool {
	ok := c.Inner(host)
	c.mu.Lock()
	c.seen = append(c.seen, host)
	c.mu.Unlock()
	if ok {
		c.Allow.Add(1)
	} else {
		c.Deny.Add(1)
	}
	return ok
}

// AsFunc returns the OnDemandAllowlist function view of this counter. Use
// this when handing the counter to certmagic.Config.OnDemand.DecisionFunc
// adapters — certmagic's signature is func(ctx, name) error, the
// OnDemandAllowlist type is func(host) bool, and this method bridges the two
// only on the predicate side.
func (c *CountingAllowlist) AsFunc() OnDemandAllowlist {
	return c.allow
}

// Seen returns a copy of the hostnames the callback was invoked with, in
// call order. Tests use this to assert certmagic reached the callback.
func (c *CountingAllowlist) Seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.seen))
	copy(out, c.seen)
	return out
}