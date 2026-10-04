package main

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// serviceAddressLookupTTL matches the 5 s service DNS TTL: an answer is
	// never served from cache for longer than a guest may cache it.
	serviceAddressLookupTTL = 5 * time.Second
	// serviceAddressLookupMaxEntries bounds the cache; past it, expired
	// entries are dropped and, if none were, the cache restarts.
	serviceAddressLookupMaxEntries = 8192
)

// newServiceAddressLookup decides which service address DNS hands a caller
// (ADR-482). It answers only when all of these hold, and otherwise leaves the
// tenant-bridge answer in place:
//   - the source address maps to one live instance on this node;
//   - that instance was created at or after the node's readiness stamp, so
//     its namespace admits the service block;
//   - the name resolves, with the HTTP mesh's preview and scenario scoping,
//     to a live app in the caller's account that holds an address.
//
// The caller lookup is never cached: host addresses are recycled when
// instances die, and a cached source-keyed answer could hand one caller
// another's target. Only the name resolution is cached, per caller app, for
// the DNS TTL; refusals are cached like answers.
func newServiceAddressLookup(store state.Store, nodeName string, log *slog.Logger) gateway.ServiceAddressLookup {
	resolve := newServiceProxyResolver(store)
	cache := &serviceAddressCache{now: time.Now, entries: make(map[serviceAddressCacheKey]serviceAddressCacheEntry)}
	return func(ctx context.Context, remoteAddr, service string) (netip.Addr, bool) {
		host := strings.TrimSpace(remoteAddr)
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		caller, err := store.ServiceAddressCallerByHostIP(ctx, nodeName, host)
		if err != nil || !caller.ServiceAddressCapable() {
			return netip.Addr{}, false
		}
		key := serviceAddressCacheKey{callerAppID: caller.AppID, service: service}
		if addr, ok, hit := cache.get(key); hit {
			return addr, ok
		}
		addr, ok := resolveServiceAddress(ctx, store, resolve, caller, service, log)
		cache.put(key, addr, ok)
		return addr, ok
	}
}

func resolveServiceAddress(ctx context.Context, store state.Store, resolve gateway.ServiceProxyResolver, caller state.ServiceAddressCaller, service string, log *slog.Logger) (netip.Addr, bool) {
	target, found, err := resolve(ctx, caller.AppID, service)
	if err != nil {
		log.Debug("gatewayd: service address lookup fell back to the bridge", "service", service, "err", err)
		return netip.Addr{}, false
	}
	if !found || target.AppID == "" {
		return netip.Addr{}, false
	}
	app, err := store.AppByID(ctx, target.AppID)
	if err != nil || app.Status == state.AppDeleted || app.AccountID != caller.AccountID {
		return netip.Addr{}, false
	}
	return api.ServiceAddressForIndex(app.ServiceAddressIndex)
}

type serviceAddressCacheKey struct{ callerAppID, service string }

type serviceAddressCacheEntry struct {
	addr    netip.Addr
	ok      bool
	expires time.Time
}

type serviceAddressCache struct {
	now     func() time.Time
	mu      sync.Mutex
	entries map[serviceAddressCacheKey]serviceAddressCacheEntry
}

func (c *serviceAddressCache) get(key serviceAddressCacheKey) (netip.Addr, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, hit := c.entries[key]
	if !hit || !c.now().Before(entry.expires) {
		return netip.Addr{}, false, false
	}
	return entry.addr, entry.ok, true
}

func (c *serviceAddressCache) put(key serviceAddressCacheKey, addr netip.Addr, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= serviceAddressLookupMaxEntries {
		for k, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= serviceAddressLookupMaxEntries {
			c.entries = make(map[serviceAddressCacheKey]serviceAddressCacheEntry)
		}
	}
	c.entries[key] = serviceAddressCacheEntry{addr: addr, ok: ok, expires: now.Add(serviceAddressLookupTTL)}
}
