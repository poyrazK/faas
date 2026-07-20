package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeCustomDomain satisfies customDomain so the allowlist tests don't have
// to import pkg/state (which transitively pulls pgx into the test binary).
type fakeCustomDomain struct {
	verifiedAt time.Time
}

func (d fakeCustomDomain) Verified() bool { return !d.verifiedAt.IsZero() }

// fakeDomainLookup is a function-table lookup satisfying OnDemandLookup.
// The struct carries the configurable state; the DomainByName method is the
// function value tests hand to NewPGAllowlist.
type fakeDomainLookup struct {
	mu     sync.Mutex
	rows   map[string]fakeCustomDomain
	err    error // when set, every lookup returns this error
	called int
}

func newFakeDomainLookup() *fakeDomainLookup {
	return &fakeDomainLookup{rows: map[string]fakeCustomDomain{}}
}

func (f *fakeDomainLookup) put(host string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := fakeCustomDomain{}
	if verified {
		d.verifiedAt = time.Now()
	}
	f.rows[host] = d
}

// DomainByName exposes the struct as an OnDemandLookup function. Tests pass
// store.DomainByName directly to NewPGAllowlist — no adapter needed.
func (f *fakeDomainLookup) DomainByName(_ context.Context, domain string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called++
	if f.err != nil {
		return nil, f.err
	}
	d, ok := f.rows[domain]
	if !ok {
		return nil, ErrNotFound
	}
	return d, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestPGAllowlist_AllowsVerifiedDomain(t *testing.T) {
	store := newFakeDomainLookup()
	store.put("jane-api.example.com", true)
	al := NewPGAllowlist(store.DomainByName, quietLogger())
	if !al("jane-api.example.com") {
		t.Fatal("verified domain must be allowlisted")
	}
}

func TestPGAllowlist_DeniesUnverified(t *testing.T) {
	store := newFakeDomainLookup()
	store.put("pending.example.com", false) // exists but TXT challenge unresolved
	al := NewPGAllowlist(store.DomainByName, quietLogger())
	if al("pending.example.com") {
		t.Fatal("unverified domain must NOT be allowlisted (spec §7: TXT gate)")
	}
}

func TestPGAllowlist_DeniesUnknown(t *testing.T) {
	store := newFakeDomainLookup()
	al := NewPGAllowlist(store.DomainByName, quietLogger())
	if al("attacker.example.com") {
		t.Fatal("unknown domain must NOT be allowlisted (cert-mint abuse vector)")
	}
}

// TestPGAllowlist_FailsClosedOnDBError — the moment Postgres has a hiccup, we
// must refuse to mint certs. The alternative (fail-open) is the canonical
// "your TLS seam leaked during an outage" failure mode.
func TestPGAllowlist_FailsClosedOnDBError(t *testing.T) {
	store := newFakeDomainLookup()
	store.err = errors.New("conn refused")
	al := NewPGAllowlist(store.DomainByName, quietLogger())
	if al("anything.example.com") {
		t.Fatal("allowlist must fail closed on DB error")
	}
}

// TestPGAllowlist_NilLookupFailsClosed — a misconfigured edge (no DB pool,
// or the wire-up step was skipped) must refuse to mint certs. Failing open
// would let any hostname through.
func TestPGAllowlist_NilLookupFailsClosed(t *testing.T) {
	al := NewPGAllowlist(nil, quietLogger())
	if al("anything.example.com") {
		t.Fatal("nil lookup must deny (fail-closed on misconfiguration)")
	}
}

func TestStaticAllowlist(t *testing.T) {
	al := StaticAllowlist("a.example.com", "b.example.com")
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if !al(host) {
			t.Errorf("static allowlist should allow %q", host)
		}
	}
	if al("c.example.com") {
		t.Error("static allowlist must deny unlisted host")
	}
}

// TestCountingAllowlist — the counter wraps the inner allowlist and records
// every invocation so tests can assert certmagic reached the decision func.
func TestCountingAllowlist(t *testing.T) {
	inner := StaticAllowlist("allowed.example.com")
	c := NewCountingAllowlist(inner)

	if !c.allow("allowed.example.com") {
		t.Error("allowed host should return true")
	}
	if c.allow("denied.example.com") {
		t.Error("denied host should return false")
	}
	if got := c.Allow.Load(); got != 1 {
		t.Errorf("Allow counter = %d, want 1", got)
	}
	if got := c.Deny.Load(); got != 1 {
		t.Errorf("Deny counter = %d, want 1", got)
	}
	got := c.Seen()
	if len(got) != 2 || got[0] != "allowed.example.com" || got[1] != "denied.example.com" {
		t.Errorf("Seen = %v, want [allowed, denied]", got)
	}
}

func TestCountingAllowlist_NilInnerDefaultsToDenyAll(t *testing.T) {
	c := NewCountingAllowlist(nil)
	if c.allow("any.example.com") {
		t.Error("nil inner should default to deny-all")
	}
	if c.Allow.Load() != 0 || c.Deny.Load() != 1 {
		t.Errorf("counters want (0,1), got (%d,%d)", c.Allow.Load(), c.Deny.Load())
	}
}
