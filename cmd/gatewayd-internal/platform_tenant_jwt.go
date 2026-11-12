package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantExternalRefStore interface {
	ResolvePlatformTenantExternalRef(ctx context.Context, accountID, externalRef string) (state.PlatformTenant, error)
}

// platformTenantExternalRefResolver maps a JWT claim to a platform tenant.
// Every request of a JWT rule that names a customer claim read the control
// plane, and a store error refused the request (bug hunt H29). Lookups are
// kept for api.PlatformTenantJWTCacheTTL, found or not, and while the store
// is unavailable the last answer is used for up to
// api.PlatformTenantJWTOutageGrace instead of refusing.
type platformTenantExternalRefResolver struct {
	store platformTenantExternalRefStore
	now   func() time.Time

	mu      sync.Mutex
	entries map[platformTenantRefKey]platformTenantRefEntry
}

type platformTenantRefKey struct{ accountID, externalRef string }

type platformTenantRefEntry struct {
	identity gateway.PlatformTenantJWTIdentity
	found    bool
	read     time.Time
}

func newPlatformTenantExternalRefResolver(store *state.PgStore) gateway.PlatformTenantExternalRefResolver {
	if store == nil {
		return nil
	}
	return newCachedPlatformTenantExternalRefResolver(store, time.Now)
}

func newCachedPlatformTenantExternalRefResolver(store platformTenantExternalRefStore, now func() time.Time) *platformTenantExternalRefResolver {
	return &platformTenantExternalRefResolver{store: store, now: now, entries: map[platformTenantRefKey]platformTenantRefEntry{}}
}

func (a *platformTenantExternalRefResolver) ResolvePlatformTenantExternalRef(ctx context.Context, accountID, externalRef string) (gateway.PlatformTenantJWTIdentity, bool, error) {
	key := platformTenantRefKey{accountID: accountID, externalRef: externalRef}
	now := a.now()
	a.mu.Lock()
	cached, ok := a.entries[key]
	a.mu.Unlock()
	if ok && now.Sub(cached.read) < api.PlatformTenantJWTCacheTTL {
		return cached.identity, cached.found, nil
	}
	tenant, err := a.store.ResolvePlatformTenantExternalRef(ctx, accountID, externalRef)
	var entry platformTenantRefEntry
	switch {
	case err == nil:
		entry = platformTenantRefEntry{identity: gateway.PlatformTenantJWTIdentity{TenantID: tenant.ID, Active: tenant.Status == state.PlatformTenantActive}, found: true, read: now}
	case errors.Is(err, state.ErrNotFound):
		entry = platformTenantRefEntry{read: now}
	case ok && now.Sub(cached.read) < api.PlatformTenantJWTOutageGrace:
		return cached.identity, cached.found, nil
	default:
		return gateway.PlatformTenantJWTIdentity{}, false, err
	}
	a.mu.Lock()
	if len(a.entries) >= api.PlatformTenantJWTCacheMax {
		a.trimLocked(now)
	}
	a.entries[key] = entry
	a.mu.Unlock()
	return entry.identity, entry.found, nil
}

// trimLocked drops entries past the outage grace, then arbitrary ones down
// to nine tenths of the cap.
func (a *platformTenantExternalRefResolver) trimLocked(now time.Time) {
	for k, e := range a.entries {
		if now.Sub(e.read) >= api.PlatformTenantJWTOutageGrace {
			delete(a.entries, k)
		}
	}
	for k := range a.entries {
		if len(a.entries) < api.PlatformTenantJWTCacheMax*9/10 {
			return
		}
		delete(a.entries, k)
	}
}
