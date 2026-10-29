//go:build !no_pg

// adr: 20261027163626 — scoped route-rule writes lock only where their
// patterns can match.
package state

import (
	"context"
	"testing"
)

// routeWriteLocks are the session locks a scoped route write of pattern takes.
func routeWriteLocks(t *testing.T, store *PgStore, account Account, pattern string) []trafficSessionLock {
	t.Helper()
	scope, err := readTrafficRouteWriteScope(t.Context(), store.pool, []string{pattern})
	if err != nil {
		t.Fatal(err)
	}
	locks, _ := trafficPolicyMutationLocks(uuidToPgtype(account.ID), true, scope)
	return locks
}

// sessionLocksConflict reports whether next is busy while held is taken.
// Rule writes also serialize on ADR-570's service_capacity_policy guard, so
// these name locks are what a scoped route write adds on top of it.
func sessionLocksConflict(t *testing.T, store *PgStore, held, next []trafficSessionLock) bool {
	t.Helper()
	_, release, busy, err := store.tryAcquireTrafficPolicySessionLocks(t.Context(), held)
	if err != nil || busy {
		t.Fatalf("hold locks: busy=%v err=%v", busy, err)
	}
	defer release(context.WithoutCancel(t.Context()))
	_, again, busy, err := store.tryAcquireTrafficPolicySessionLocks(t.Context(), next)
	if again != nil {
		again(context.WithoutCancel(t.Context()))
	}
	if err != nil {
		t.Fatal(err)
	}
	return busy
}

func TestPgTrafficScopedRouteWritesLockTheirScope(t *testing.T) {
	store, _, first, _ := trafficHostPGFixture(t)
	second, _ := globalTrafficPeer(t, store, "scoped-route-peer")
	for _, tc := range []struct {
		held, next string
		conflict   bool
	}{
		{"*.a.example.test", "*.b.example.test", false},
		{"api.example.test", "web.example.test", false},
		{"API.Example.Test", "web.example.test", false},
		{"*.example.test", "api.example.test", true},
		{"api.example.test", "*.example.test", true},
		{"promo-*.example.test", "api.example.test", true},
		{"promo-*.example.test", "api.example.org", false},
		{"*", "api.example.org", true},
		{"api.*", "api.example.org", true},
		{"@app", "api.example.org", true},
	} {
		t.Run(tc.held+" then "+tc.next, func(t *testing.T) {
			held, next := routeWriteLocks(t, store, first, tc.held), routeWriteLocks(t, store, second, tc.next)
			if got := sessionLocksConflict(t, store, held, next); got != tc.conflict {
				t.Fatalf("conflict=%v, want %v", got, tc.conflict)
			}
		})
	}
	t.Run("fleet-wide route write holds the root", func(t *testing.T) {
		global, _ := trafficPolicyMutationLocks(uuidToPgtype(first.ID), true, nil)
		if !sessionLocksConflict(t, store, global, routeWriteLocks(t, store, second, "*.b.example.test")) {
			t.Fatal("a scoped route write ran beside the previous release's")
		}
	})
}

// A scoped route write and a scoped binding write conflict only when the
// route can match a hostname the binding changes.
func TestPgTrafficScopedRouteAndBindingWritesShareNameLocks(t *testing.T) {
	store, _, first, _ := trafficHostPGFixture(t)
	route := routeWriteLocks(t, store, first, "*.example.test")
	for _, tc := range []struct {
		host     string
		conflict bool
	}{
		{"shop.example.test", true},
		{"example.test", false},
		{"shop.example.org", false},
	} {
		if got := sessionLocksConflict(t, store, route, trafficNameLocks([]string{tc.host}, nil)); got != tc.conflict {
			t.Fatalf("binding of %s: conflict=%v, want %v", tc.host, got, tc.conflict)
		}
	}
}
