// adr: 570
// Package publicrouting keeps each gateway's refreshed view of the routing
// state a public request needs: the app's owner, its deployment roster and
// its project's release graph (ADR-20261027085556), and what host, revision
// and release pins check (ADR-20261028095914).
//
// A request admits from the view an app's last successful read left, when
// that read is within api.TrafficPublicRoutingOutageGrace. Otherwise the app
// is read before admission. The refresher re-reads every tracked app each
// api.TrafficPublicRoutingRefreshInterval, and an app at once when a
// notification names it. While the store cannot be read, views keep
// admitting until the grace runs out. A store answer that refuses an app,
// such as an unverified owner or an oversized roster, drops its view at once.
package publicrouting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Key = state.PublicRoutingViewKey

// Store is state.PgStore's batched routing read.
type Store = state.PublicRoutingViewStore

type entry struct {
	view state.PublicRoutingView
	// refused is the store's last answer when it refused the key. It is
	// never served: a refusal is confirmed by the store on every request.
	refused error
	// readAt is when the read that produced the answer began; lastUsed is
	// when a request last asked for the key.
	readAt, lastUsed time.Time
}

type Registry struct {
	store     Store
	log       *slog.Logger
	now       func() time.Time
	grace     time.Duration
	mu        sync.Mutex
	entries   map[Key]*entry
	dirty     map[string]struct{}
	dirtyAll  bool
	failing   bool
	refreshMu sync.Mutex
	reads     singleflight.Group
	wake      chan struct{}
}

func New(store Store) *Registry {
	return &Registry{store: store, now: time.Now, grace: api.TrafficPublicRoutingOutageGrace,
		entries: make(map[Key]*entry), dirty: make(map[string]struct{}), wake: make(chan struct{}, 1)}
}

// WithClock replaces the registry's clock; tests use it to cross the warm
// and outage bounds without waiting.
func (r *Registry) WithClock(now func() time.Time) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
	return r
}

// WithOutageGrace shortens the grace for process-level outage tests.
func (r *Registry) WithOutageGrace(grace time.Duration) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.grace = grace
	return r
}

// WithLogger logs when refreshes start failing and when they recover.
func (r *Registry) WithLogger(log *slog.Logger) *Registry {
	r.log = log
	return r
}

// View returns the key's view, from memory when its last read is within the
// outage grace, otherwise from a store read made now. Concurrent reads of one
// key share a single store read.
func (r *Registry) View(ctx context.Context, key Key) (state.PublicRoutingView, error) {
	if r == nil || r.store == nil {
		return state.PublicRoutingView{}, errors.New("public routing view is unavailable")
	}
	if !validKey(key) {
		return state.PublicRoutingView{}, errors.New("public routing owner is unavailable")
	}
	if view, ok := r.cached(key); ok {
		return view, nil
	}
	result := r.reads.DoChan(fmt.Sprintf("%q", key), func() (any, error) {
		// One caller's cancellation must not fail the others' shared read.
		read, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TrafficPublicRoutingReadTimeout)
		defer cancel()
		return r.readKeys(read, []Key{key})
	})
	select {
	case <-ctx.Done():
		return state.PublicRoutingView{}, ctx.Err()
	case done := <-result:
		if done.Err != nil {
			return state.PublicRoutingView{}, done.Err
		}
		answer := done.Val.(map[Key]state.PublicRoutingViewResult)[key]
		if answer.Err != nil {
			return state.PublicRoutingView{}, answer.Err
		}
		return answer.View, nil
	}
}

func validKey(key Key) bool {
	return canonicalID(key.AppID) && canonicalID(key.AccountID) && (key.ProjectID == "" || canonicalID(key.ProjectID))
}

func canonicalID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func (r *Registry) cached(key Key) (state.PublicRoutingView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.entries[key]
	if current == nil {
		return state.PublicRoutingView{}, false
	}
	now := r.now()
	current.lastUsed = now
	if current.refused != nil || now.Sub(current.readAt) > r.grace {
		return state.PublicRoutingView{}, false
	}
	return current.view, true
}

// readKeys reads keys and records the answers. An answer replaces only an
// older one, so a slow read cannot bring back a view a newer read refused.
func (r *Registry) readKeys(ctx context.Context, keys []Key) (map[Key]state.PublicRoutingViewResult, error) {
	r.mu.Lock()
	started := r.now()
	r.mu.Unlock()
	results, err := r.store.ReadPublicRoutingViews(ctx, keys)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range keys {
		answer, ok := results[key]
		if !ok {
			answer = state.PublicRoutingViewResult{Err: errors.New("public routing view omitted a key")}
			results[key] = answer
		}
		current := r.entries[key]
		if current == nil {
			r.evictLocked()
			current = &entry{lastUsed: started}
			r.entries[key] = current
		}
		if !started.Before(current.readAt) {
			current.view, current.refused, current.readAt = answer.View, answer.Err, started
		}
	}
	return results, nil
}

// evictLocked makes room for one view by dropping the least recently used.
func (r *Registry) evictLocked() {
	for len(r.entries) >= api.TrafficPublicRoutingMaxViews {
		var oldest Key
		var oldestAt time.Time
		found := false
		for key, current := range r.entries {
			if !found || current.lastUsed.Before(oldestAt) {
				oldest, oldestAt, found = key, current.lastUsed, true
			}
		}
		delete(r.entries, oldest)
	}
}

// Refresh re-reads every tracked view, after forgetting those no request used
// within api.TrafficPublicRoutingWarmTTL. A store failure leaves the views in
// place; each admits until its own read is older than the outage grace.
func (r *Registry) Refresh(ctx context.Context) error {
	return r.refresh(ctx, func(Key) bool { return true })
}

// RefreshApp synchronously re-reads the app's tracked views. A gateway calls
// it before acknowledging a deployment route change.
func (r *Registry) RefreshApp(ctx context.Context, app string) error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.refresh(ctx, func(key Key) bool { return key.AppID == app })
}

func (r *Registry) refresh(ctx context.Context, include func(Key) bool) error {
	if r == nil || r.store == nil {
		return errors.New("public routing view is unavailable")
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.Lock()
	now := r.now()
	keys := make([]Key, 0, len(r.entries))
	for key, current := range r.entries {
		if now.Sub(current.lastUsed) > api.TrafficPublicRoutingWarmTTL {
			delete(r.entries, key)
			continue
		}
		if include(key) {
			keys = append(keys, key)
		}
	}
	r.mu.Unlock()
	if len(keys) == 0 {
		return nil
	}
	read, cancel := context.WithTimeout(ctx, api.TrafficPublicRoutingRefreshTimeout)
	defer cancel()
	_, err := r.readKeys(read, keys)
	r.logTransition(err, len(keys))
	return err
}

func (r *Registry) logTransition(err error, keys int) {
	r.mu.Lock()
	changed := r.failing != (err != nil)
	r.failing = err != nil
	r.mu.Unlock()
	if r.log == nil || !changed {
		return
	}
	if err != nil {
		r.log.Warn("gateway: public routing view refresh failing; admitting from verified views until the outage grace",
			"views", keys, "grace", r.grace, "err", err)
		return
	}
	r.log.Info("gateway: public routing view refresh recovered", "views", keys)
}

// RequestRefresh schedules a prompt re-read of the app's views. An empty app
// re-reads every view. Notifications are wake-ups only; the refresher reads
// the store.
func (r *Registry) RequestRefresh(app string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if app == "" {
		r.dirtyAll = true
	} else {
		r.dirty[app] = struct{}{}
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Registry) takeDirty() (map[string]struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	apps, all := r.dirty, r.dirtyAll
	r.dirty, r.dirtyAll = make(map[string]struct{}), false
	return apps, all
}

// Run refreshes every interval, and the notified apps on a wake-up.
func (r *Registry) Run(ctx context.Context) {
	ticker := time.NewTicker(api.TrafficPublicRoutingRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.takeDirty()
			_ = r.Refresh(ctx)
		case <-r.wake:
			apps, all := r.takeDirty()
			if all {
				_ = r.Refresh(ctx)
				continue
			}
			_ = r.refresh(ctx, func(key Key) bool {
				_, notified := apps[key.AppID]
				return notified
			})
		}
	}
}

// Tracked reports how many views the registry holds.
func (r *Registry) Tracked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
