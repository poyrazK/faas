// adr: 570
// Package trafficrevocation fences admitted exchanges using authoritative
// security generations independently of their immutable traffic policy.
//
// A gateway admits from its own refreshed view of those generations
// (ADR-20261026160000): a scope it tracked recently is admitted from the
// generation its refresher last read, so warm traffic reads the store only
// in the background. A scope it does not track, or one not verified within
// api.TrafficSecurityOutageGrace, is read before admission. While the store
// cannot be read, admitted traffic and warm scopes ride on their last
// verified generations until that grace runs out.
package trafficrevocation

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrRevoked     = errors.New("traffic security generation revoked")
	ErrUnavailable = errors.New("traffic security verification unavailable")
	ErrCapacity    = errors.New("traffic security tracking capacity exhausted")
	// errInvalidSnapshot is a store answer that cannot be a generation. It is
	// not an outage: it fails verified traffic at once, with no outage grace.
	errInvalidSnapshot = fmt.Errorf("%w: invalid generation in store result", ErrUnavailable)
)

type Scope struct{ Kind, ID string }
type State struct {
	Revision int64
	Revoked  bool
}

type CapacityError struct {
	Resource        string
	Limit, Observed int
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("%s: %s limit=%d observed=%d", ErrCapacity, e.Resource, e.Limit, e.Observed)
}

func (*CapacityError) Unwrap() error { return ErrCapacity }

// Read returns a verified snapshot. A scope absent from that snapshot has
// generation zero and no recorded revoke. Callers supply owner-resolved IDs;
// this store does not authenticate arbitrary identities.
type Store interface {
	Read(context.Context, []Scope) (map[Scope]State, error)
}

type exchange struct {
	states map[Scope]State
	cancel context.CancelCauseFunc
}

type Registry struct {
	store   Store
	now     func() time.Time
	mu      sync.Mutex
	active  map[*exchange]struct{}
	latest  map[Scope]State
	members map[Scope]map[*exchange]struct{}
	// verified is when the store last confirmed latest[scope]; lastUsed is
	// when a request last held the scope. A scope with no members stays
	// tracked (warm) until api.TrafficSecurityWarmScopeTTL after lastUsed.
	verified map[Scope]time.Time
	lastUsed map[Scope]time.Time
	// refreshedAt is the last successful refresh. A failing store cancels
	// admitted traffic only once it is api.TrafficSecurityOutageGrace old.
	refreshedAt time.Time
	refreshMu   sync.Mutex
	wake        chan struct{}
	closed      bool
}

func New(store Store) *Registry {
	r := &Registry{store: store, now: time.Now, active: make(map[*exchange]struct{}),
		latest: make(map[Scope]State), members: make(map[Scope]map[*exchange]struct{}),
		verified: make(map[Scope]time.Time), lastUsed: make(map[Scope]time.Time), wake: make(chan struct{}, 1)}
	r.refreshedAt = r.now()
	return r
}

// WithClock replaces the registry's clock; tests use it to cross the warm
// and outage bounds without waiting.
func (r *Registry) WithClock(now func() time.Time) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
	r.refreshedAt = now()
	return r
}

// Admit verifies before enrolling. The release function only unregisters;
// its owner cancels the lifetime context after final response writes finish.
func (r *Registry) Admit(ctx context.Context, scopes []Scope, cancel context.CancelCauseFunc) (func(), error) {
	_, release, err := r.AdmitSnapshot(ctx, scopes, cancel)
	return release, err
}

// AdmitSnapshot returns a defensive copy of the exact admitted generations.
// A forwarding hop must transfer these values, never a later independent read.
func (r *Registry) AdmitSnapshot(ctx context.Context, scopes []Scope, cancel context.CancelCauseFunc) (map[Scope]State, func(), error) {
	return r.admit(ctx, scopes, nil, cancel)
}

// AdmitAt verifies a preceding hop's baseline before independently enrolling.
// A changed generation refuses even if a revoke has already been released.
func (r *Registry) AdmitAt(ctx context.Context, expected map[Scope]State, cancel context.CancelCauseFunc) (func(), error) {
	baseline := maps.Clone(expected)
	scopes := make([]Scope, 0, len(baseline))
	for scope, state := range baseline {
		if state.Revision < 0 || state.Revoked {
			return nil, ErrUnavailable
		}
		scopes = append(scopes, scope)
	}
	_, release, err := r.admit(ctx, scopes, baseline, cancel)
	return release, err
}

func (r *Registry) admit(ctx context.Context, scopes []Scope, expected map[Scope]State, cancel context.CancelCauseFunc) (map[Scope]State, func(), error) {
	if r == nil || r.store == nil || cancel == nil {
		return nil, nil, ErrUnavailable
	}
	scopes, err := normalize(scopes)
	if err != nil {
		return nil, nil, err
	}
	states, fromStore := r.cached(scopes, expected), false
	if states == nil {
		if states, err = r.read(ctx, scopes); err != nil {
			return nil, nil, err
		}
		fromStore = true
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	lease := &exchange{states: make(map[Scope]State, len(scopes)), cancel: cancel}
	r.mu.Lock()
	defer r.mu.Unlock()
	admissionErr := r.validateAdmission(scopes, states)
	if expected != nil {
		if err := verifyBaseline(states, expected); err != nil {
			admissionErr = err
		}
	}
	// A store read can discover a revoke before the periodic repair pass.
	if fromStore {
		r.apply(states)
	}
	if admissionErr != nil {
		return nil, nil, admissionErr
	}
	now := r.now()
	for _, scope := range scopes {
		if _, tracked := r.latest[scope]; !tracked {
			r.evictWarmLocked(1)
		}
		lease.states[scope] = states[scope]
		r.latest[scope] = states[scope]
		if fromStore {
			r.verified[scope] = now
		}
		r.lastUsed[scope] = now
		if r.members[scope] == nil {
			r.members[scope] = make(map[*exchange]struct{})
		}
		r.members[scope][lease] = struct{}{}
	}
	r.active[lease] = struct{}{}
	var once sync.Once
	return maps.Clone(lease.states), func() { once.Do(func() { r.release(lease) }) }, nil
}

// cached returns the scopes' last verified generations when every scope is
// tracked and was verified within api.TrafficSecurityOutageGrace, and nil when
// any scope needs a store read. A preceding hop's baseline newer than the
// cache also needs one: the cache may lag a generation that hop already read.
// So does a revoked scope: a refusal is always confirmed by the store, which
// also lets a release admit again at once rather than at the next refresh.
func (r *Registry) cached(scopes []Scope, expected map[Scope]State) map[Scope]State {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	states := make(map[Scope]State, len(scopes))
	for _, scope := range scopes {
		state, tracked := r.latest[scope]
		at, verified := r.verified[scope]
		if !tracked || !verified || state.Revoked || now.Sub(at) > api.TrafficSecurityOutageGrace {
			return nil
		}
		if before, ok := expected[scope]; ok && before.Revision > state.Revision {
			return nil
		}
		states[scope] = state
	}
	return states
}

// evictWarmLocked makes room for n new scopes by dropping the least recently
// used warm scopes, so a refresh never reads more than the store's bound.
// Scopes with members are never evicted; validateAdmission bounds those.
func (r *Registry) evictWarmLocked(n int) {
	for len(r.latest)+n > api.TrafficSecurityMaxScopes {
		var oldest Scope
		var oldestAt time.Time
		found := false
		for scope := range r.latest {
			if len(r.members[scope]) > 0 {
				continue
			}
			if at := r.lastUsed[scope]; !found || at.Before(oldestAt) {
				oldest, oldestAt, found = scope, at, true
			}
		}
		if !found {
			return
		}
		r.forgetLocked(oldest)
	}
}

func (r *Registry) forgetLocked(scope Scope) {
	delete(r.latest, scope)
	delete(r.verified, scope)
	delete(r.lastUsed, scope)
}

func verifyBaseline(states, expected map[Scope]State) error {
	for scope, before := range expected {
		after := states[scope]
		if after.Revision < before.Revision || after.Revision == before.Revision && after != before {
			return ErrUnavailable
		}
		if after != before {
			return ErrRevoked
		}
	}
	return nil
}

func (r *Registry) validateAdmission(scopes []Scope, states map[Scope]State) error {
	if r.closed {
		return ErrUnavailable
	}
	if len(r.active) >= api.TrafficSecurityMaxExchanges {
		return &CapacityError{Resource: "exchanges", Limit: api.TrafficSecurityMaxExchanges, Observed: len(r.active) + 1}
	}
	newScopes := 0
	for _, scope := range scopes {
		state := states[scope]
		if prior, ok := r.latest[scope]; ok {
			if state.Revision < prior.Revision || state.Revision == prior.Revision && state != prior {
				return ErrUnavailable
			}
		}
		if len(r.members[scope]) == 0 {
			newScopes++
		}
		if state.Revoked {
			return ErrRevoked
		}
	}
	if len(r.members)+newScopes > api.TrafficSecurityMaxScopes {
		return &CapacityError{Resource: "security_scopes", Limit: api.TrafficSecurityMaxScopes, Observed: len(r.members) + newScopes}
	}
	return nil
}

func normalize(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 || len(scopes) > api.TrafficSecurityMaxRequestScopes {
		return nil, ErrUnavailable
	}
	unique := make(map[Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		if strings.TrimSpace(scope.ID) == "" || (scope.Kind != "account" && scope.Kind != "app" && scope.Kind != "deployment") {
			return nil, ErrUnavailable
		}
		unique[scope] = struct{}{}
	}
	result := make([]Scope, 0, len(unique))
	for scope := range unique {
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Kind < result[j].Kind || result[i].Kind == result[j].Kind && result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *Registry) read(ctx context.Context, scopes []Scope) (map[Scope]State, error) {
	bounded, cancel := context.WithTimeout(ctx, api.TrafficSecurityStoreTimeout)
	defer cancel()
	states, err := r.store.Read(bounded, scopes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	verified := make(map[Scope]State, len(scopes))
	for _, scope := range scopes {
		state := states[scope]
		if state.Revision < 0 || state.Revoked && state.Revision == 0 {
			return nil, errInvalidSnapshot
		}
		verified[scope] = state
	}
	return verified, nil
}

// release unregisters the exchange. Its scopes stay tracked as warm scopes,
// refreshed in the background, so the app's next request needs no read.
func (r *Registry) release(lease *exchange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, lease)
	now := r.now()
	for scope := range lease.states {
		delete(r.members[scope], lease)
		if len(r.members[scope]) == 0 {
			delete(r.members, scope)
		}
		r.lastUsed[scope] = now
	}
}

// Refresh is serialized: an older read must not overwrite a newer generation.
// It reads every tracked scope, active and warm, and forgets warm scopes idle
// past api.TrafficSecurityWarmScopeTTL. A store error cancels all tracked
// exchanges, including detached sessions, once the last successful refresh is
// older than api.TrafficSecurityOutageGrace; until then they keep running on
// their verified generations. A store answer that cannot be a generation
// cancels them at once.
func (r *Registry) Refresh(ctx context.Context) error {
	if r == nil || r.store == nil {
		return ErrUnavailable
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.Lock()
	now := r.now()
	scopes := make([]Scope, 0, len(r.latest))
	for scope := range r.latest {
		if len(r.members[scope]) == 0 && now.Sub(r.lastUsed[scope]) > api.TrafficSecurityWarmScopeTTL {
			r.forgetLocked(scope)
			continue
		}
		scopes = append(scopes, scope)
	}
	r.mu.Unlock()
	if len(scopes) == 0 {
		r.mu.Lock()
		r.refreshedAt = now
		r.mu.Unlock()
		return nil
	}
	states, err := r.read(ctx, scopes)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		if errors.Is(err, errInvalidSnapshot) || r.now().Sub(r.refreshedAt) > api.TrafficSecurityOutageGrace {
			r.cancelAll(err)
		}
		return err
	}
	r.apply(states)
	r.refreshedAt = now
	for scope, state := range states {
		if latest, ok := r.latest[scope]; ok && latest == state {
			r.verified[scope] = now
		} else {
			// An inconsistent read leaves the cache unverified, so the next
			// admission reads the store and validateAdmission refuses it.
			delete(r.verified, scope)
		}
	}
	return nil
}

// apply requires mu. CancelCauseFunc is non-blocking; owners perform cleanup
// independently and retain their registration until actual ownership ends.
func (r *Registry) apply(states map[Scope]State) {
	for scope, after := range states {
		if prior, ok := r.latest[scope]; ok && after == prior {
			continue // the common admission path does not scan unchanged owners
		}
		for lease := range r.members[scope] {
			before := lease.states[scope]
			if after.Revision < before.Revision || after.Revision == before.Revision && after != before {
				lease.cancel(ErrUnavailable)
				continue
			}
			if after.Revoked || after.Revision != before.Revision {
				lease.cancel(ErrRevoked)
			}
		}
	}
	for scope, state := range states {
		if prior, ok := r.latest[scope]; ok && (state.Revision > prior.Revision || state == prior) {
			r.latest[scope] = state
		}
	}
}

func (r *Registry) cancelAll(cause error) {
	for lease := range r.active {
		lease.cancel(cause)
	}
}

func (r *Registry) RequestRefresh() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Registry) Run(ctx context.Context) {
	ticker := time.NewTicker(api.TrafficSecurityRefreshInterval)
	defer ticker.Stop()
	defer r.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
		_ = r.Refresh(ctx)
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.cancelAll(ErrUnavailable)
}

func (r *Registry) Tracked() (exchanges, scopes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active), len(r.members)
}
