package outbound

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ManagedKey is one provider key of an integration's pool (ADR-991). ID is
// "primary" for the integration's credential or the extra key's UUID. Value
// is the sealed credential's plaintext and is never logged or returned.
type ManagedKey struct {
	ID    string
	Value string
}

// PrimaryManagedKeyID names the integration's own credential in a pool.
const PrimaryManagedKeyID = "primary"

// ManagedKeyPoolResolver supplies every key of a customer-sealed
// integration, primary first.
type ManagedKeyPoolResolver interface {
	Authorizations(ctx context.Context, integrationID string) ([]ManagedKey, error)
}

// keyPoolState is this process's view of which keys are cooling down after
// a 429 and where each integration's round robin stands. It is
// deliberately local: a key rotates away on its first 429 (ADR-991).
type keyPoolState struct {
	mu        sync.Mutex
	cooldowns map[string]time.Time
	next      map[string]uint64
}

func keyPoolKey(integrationID, keyID string) string { return integrationID + "/" + keyID }

// order returns the keys to try for one call: available keys in round-robin
// order, then cooling keys by soonest expiry, so a call still goes out when
// every key is cooling.
func (s *keyPoolState) order(integrationID string, keys []ManagedKey, now time.Time) []ManagedKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cooldowns == nil {
		s.cooldowns, s.next = make(map[string]time.Time), make(map[string]uint64)
	}
	start := int(s.next[integrationID] % uint64(len(keys)))
	s.next[integrationID]++
	var available []ManagedKey
	type cooling struct {
		key   ManagedKey
		until time.Time
	}
	var waiting []cooling
	for i := range keys {
		key := keys[(start+i)%len(keys)]
		poolKey := keyPoolKey(integrationID, key.ID)
		until, cooled := s.cooldowns[poolKey]
		if cooled && !now.Before(until) {
			delete(s.cooldowns, poolKey)
			cooled = false
		}
		if cooled {
			waiting = append(waiting, cooling{key, until})
		} else {
			available = append(available, key)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].until.Before(waiting[j].until) })
	for _, entry := range waiting {
		available = append(available, entry.key)
	}
	return available
}

func (s *keyPoolState) cool(integrationID, keyID string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cooldowns == nil {
		s.cooldowns, s.next = make(map[string]time.Time), make(map[string]uint64)
	}
	if existing, ok := s.cooldowns[keyPoolKey(integrationID, keyID)]; !ok || until.After(existing) {
		s.cooldowns[keyPoolKey(integrationID, keyID)] = until
	}
}

// credentialRing rotates one call through its ordered keys on 429s.
type credentialRing struct {
	integrationID, metricIntegrationID string
	keys                               []ManagedKey
	current                            int
	apply                              func(*http.Request, string) error
	pools                              *keyPoolState
	metrics                            *Metrics
}

// rotate cools the key that drew a 429 and puts the next key on req. It
// reports false when no other key remains.
func (r *credentialRing) rotate(req *http.Request, retryAfter time.Duration) bool {
	if r == nil || len(r.keys) < 2 {
		return false
	}
	if retryAfter <= 0 {
		retryAfter = time.Duration(api.OutboundCredentialKeyDefaultCooldownSeconds) * time.Second
	}
	r.pools.cool(r.integrationID, r.keys[r.current].ID, time.Now().Add(min(retryAfter, maxProviderCooldown)))
	r.metrics.ObserveCredentialKey(r.metricIntegrationID, "cooldown")
	if r.current+1 >= len(r.keys) {
		r.metrics.ObserveCredentialKey(r.metricIntegrationID, "exhausted")
		return false
	}
	r.current++
	if err := r.apply(req, r.keys[r.current].Value); err != nil {
		return false
	}
	r.metrics.ObserveCredentialKey(r.metricIntegrationID, "rotated")
	return true
}

// validManagedKeys drops keys whose value is not a usable credential.
func validManagedKeys(keys []ManagedKey) []ManagedKey {
	out := keys[:0:0]
	for _, key := range keys {
		if key.ID != "" && ValidManagedAuthorization(key.Value) {
			out = append(out, key)
		}
	}
	return out
}
