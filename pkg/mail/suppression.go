package mail

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SuppressionChecker is the narrow interface pkg/mail consumes from
// the state layer. The interface lives in the leaf package so pkg/mail
// does not import pkg/state (that would drag Postgres/sqlc into every
// call path); the apid / meterd main wires Store to this shape via
// a one-line adapter that calls Store.IsMailSuppressed.
//
// Why an interface at all: the in-process cache + decorator are
// testable in isolation with a fake SuppressionChecker — no DB, no
// harness.
type SuppressionChecker interface {
	IsMailSuppressed(ctx context.Context, email string) (bool, error)
}

// SuppressingSender drops sends whose recipient is on the active
// suppression list. It is the outermost decorator in the stack so
// suppression costs zero HTTP attempts — the retry decorator never
// gets a chance to fire on an address the platform has already
// decided should not receive mail.
//
// A suppressed send returns nil (a deliberate drop), not an error.
// Returning an error would re-trigger RetryingSender on the way out,
// which would issue a 429-idempotency-keyed call to Resend for an
// address we already know not to mail. Drop silently. The bounce
// row that produced the suppression is the audit trail; nothing
// else needs to happen.
type SuppressingSender struct {
	// Inner is the wrapped Sender. Required.
	Inner Sender
	// Store is the suppression checker. Required.
	Store SuppressionChecker
	// Log is the structured logger. nil falls back to slog.Default().
	Log *slog.Logger
	// Metrics is the optional observer. nil is safe — the seam
	// already tolerates a nil Metrics on the inner decorator.
	Metrics Metrics
	// CacheTTL is the lifetime of an in-process negative + positive
	// result. Zero or negative falls back to
	// api.MailSuppressionCacheTTLSeconds. The cache is keyed on
	// lower(email) to match the partial index on the table.
	CacheTTL time.Duration
	// Now is the clock. nil falls back to time.Now. Tests inject
	// a deterministic clock so the TTL expiry is reproducible.
	Now func() time.Time

	// cache is the process-local memo. Lazily initialised so a
	// SuppressingSender constructed in a test (without a call to
	// Send) does not pay for an empty map allocation. cache is
	// nil-safe via the helpers cache()/ensureCache() below.
	cache *suppressionCache
}

// cacheEntry is one row of the in-process cache. We cache the
// "suppressed" decision (true) AND the "not suppressed" decision
// (false) — caching the negative saves a DB round-trip for every
// non-suppressed send (the common case) and the 60s staleness is
// acceptable because real-time accuracy is the bounce handler's
// job, not the sending path's.
type cacheEntry struct {
	suppressed bool
	expiresAt  time.Time
}

// suppressionCache is the per-SuppressingSender cache. The struct
// is built around a plain map protected by a mutex: the cache is
// process-local (restarts wipe it, which is fine — the Store is
// the source of truth), and the expected hit rate (>99% for
// non-suppressed addresses) means we never need a LRU eviction
// policy.
type suppressionCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

func newSuppressionCache() *suppressionCache {
	return &suppressionCache{entries: map[string]cacheEntry{}}
}

// get returns the cached decision if one is still fresh. ok=false
// means "miss or expired"; the caller should ask the Store.
func (c *suppressionCache) get(key string, now time.Time) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	if !now.Before(e.expiresAt) {
		delete(c.entries, key)
		return cacheEntry{}, false
	}
	return e, true
}

// put records a decision with its absolute expiry timestamp.
func (c *suppressionCache) put(key string, entry cacheEntry) {
	c.mu.Lock()
	c.entries[key] = entry
	c.mu.Unlock()
}

// ensureCache lazy-initialises the cache so a sender constructed
// in a test (without ever calling Send) does not pay for an empty
// map allocation, and so the field does not need to be exported.
func (s *SuppressingSender) ensureCache() *suppressionCache {
	if s.cache == nil {
		s.cache = newSuppressionCache()
	}
	return s.cache
}

// Send drops suppressed recipients and otherwise forwards the
// message unchanged. The cache lookup uses lower(email) so the
// map key matches the partial index in the database — a customer
// who types "Alice@Example.com" today and "alice@example.com"
// tomorrow hits the same cache row.
func (s *SuppressingSender) Send(ctx context.Context, msg Message) error {
	if s.Inner == nil {
		return errors.New("mail: SuppressingSender.Inner is nil")
	}
	if s.Store == nil {
		// Fail-closed: a misconfigured wiring (Store not injected)
		// must NOT bypass the suppression check. Returning the
		// raw inner Send would let unsuppressed mail escape during
		// a deploy half-state. The caller will see this error and
		// the message stays in the journal.
		return errors.New("mail: SuppressingSender.Store is nil")
	}
	log := s.log()
	now := s.now()

	// An unclaimed anonymous-deploy account's placeholder address
	// (ADR-20261124080000) is in the reserved .invalid domain: never send
	// to it, whatever produced the message.
	msg.To = dropUnclaimedPlaceholders(msg.To)
	if len(msg.To) == 0 {
		log.Debug("mail.unclaimed_placeholder.drop", "subject", msg.Subject)
		return nil
	}

	// Every address must clear the check before send. A message
	// with mixed To/Cc/Bcc-lists is rare (today all senders pass
	// a single recipient) but we honour the contract anyway.
	for _, addr := range msg.To {
		key := strings.ToLower(addr)
		if key == "" {
			continue
		}
		if s.suppressed(ctx, key, now()) {
			log.Info("mail.suppressed.drop",
				"recipient", addr,
				"transport", "suppressed")
			if s.Metrics != nil {
				s.Metrics.RecordFailure(ReasonSuppressed)
			}
			// Single recipient suppressed → drop the entire
			// message rather than send a half-cleared To-list.
			// Rationale: a billing/security mail with one
			// suppressed recipient is the operationally
			// important case, and partial delivery risks the
			// remaining recipient seeing a confusing "Bcc"
			// context. The bounce row is the audit trail.
			return nil
		}
	}
	return s.Inner.Send(ctx, msg)
}

// suppressed returns the cached decision if fresh, otherwise asks
// the Store and caches the result.
func (s *SuppressingSender) suppressed(ctx context.Context, key string, now time.Time) bool {
	if e, ok := s.ensureCache().get(key, now); ok {
		return e.suppressed
	}
	hit, err := s.Store.IsMailSuppressed(ctx, key)
	if err != nil {
		// A failed check must NOT silently let mail through —
		// that would be a fail-OPEN escape hatch. The Store
		// failure here is the same fail mode as the daemon
		// refusing to boot: the platform cannot verify the
		// recipient is safe, so we treat the address as
		// suppressed for this attempt. The bounce row
		// the *next* time the address is checked will
		// re-populate the cache.
		s.log().Warn("mail.suppressed.check_failed",
			"recipient", key,
			"err", err.Error())
		return true
	}
	ttl := s.ttl()
	s.ensureCache().put(key, cacheEntry{
		suppressed: hit,
		expiresAt:  now.Add(ttl),
	})
	return hit
}

func (s *SuppressingSender) ttl() time.Duration {
	if s.CacheTTL > 0 {
		return s.CacheTTL
	}
	return time.Duration(api.MailSuppressionCacheTTLSeconds) * time.Second
}

func (s *SuppressingSender) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *SuppressingSender) now() func() time.Time {
	if s.Now != nil {
		return s.Now
	}
	return time.Now
}

// dropUnclaimedPlaceholders removes unclaimed accounts' placeholder
// addresses from a recipient list.
func dropUnclaimedPlaceholders(to []string) []string {
	out := to[:0:0]
	for _, addr := range to {
		if api.IsUnclaimedPlaceholderEmail(addr) {
			continue
		}
		out = append(out, addr)
	}
	return out
}
