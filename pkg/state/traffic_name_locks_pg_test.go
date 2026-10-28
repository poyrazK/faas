//go:build !no_pg

// adr: 20261027163626
package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type trafficLockScope struct {
	hosts, subtrees []string
	legacy          bool // the previous release: the global route key, exclusive
}

func (s trafficLockScope) locks() []trafficSessionLock {
	if s.legacy {
		return []trafficSessionLock{{Key: globalTrafficRoutesLock}}
	}
	return trafficNameLocks(s.hosts, s.subtrees)
}

// Two binding writes run together exactly when neither's names lie inside
// the other's, and the previous release's global lock still excludes both.
func TestPgTrafficNameLockConflicts(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	host := func(names ...string) trafficLockScope { return trafficLockScope{hosts: names} }
	tree := func(names ...string) trafficLockScope { return trafficLockScope{subtrees: names} }
	legacy := trafficLockScope{legacy: true}
	for _, tc := range []struct {
		name     string
		held, at trafficLockScope
		conflict bool
	}{
		{"same host", host("a.apps.example"), host("A.apps.example"), true},
		{"sibling hosts", host("a.apps.example"), host("b.apps.example"), false},
		{"host and its apex", host("www.example.com"), host("example.com"), false},
		{"host inside a subtree", tree("example.com"), host("www.example.com"), true},
		{"host deep inside a subtree", host("a.b.example.com"), tree("example.com"), true},
		{"subtree apex is not inside it", tree("example.com"), host("example.com"), false},
		{"unrelated subtree", tree("example.org"), host("www.example.com"), false},
		{"nested subtrees", tree("example.com"), tree("api.example.com"), true},
		{"sibling subtrees", tree("a.example.com"), tree("b.example.com"), false},
		{"every hostname", tree(""), host("x.test"), true},
		{"previous release holds the global lock", legacy, host("x.test"), true},
		{"previous release waits for a scoped writer", host("x.test"), legacy, true},
		{"previous releases exclude each other", legacy, legacy, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, release, busy, err := store.tryAcquireTrafficPolicySessionLocks(t.Context(), tc.held.locks())
			if err != nil || busy {
				t.Fatalf("hold: busy=%v err=%v", busy, err)
			}
			defer release(t.Context())
			_, other, busy, err := store.tryAcquireTrafficPolicySessionLocks(t.Context(), tc.at.locks())
			if err != nil {
				t.Fatal(err)
			}
			if !busy {
				other(t.Context())
			}
			if busy != tc.conflict {
				t.Fatalf("conflict = %v, want %v", busy, tc.conflict)
			}
		})
	}
	// A busy attempt releases what it took, so the holder's release frees all.
	_, release, busy, err := store.tryAcquireTrafficPolicySessionLocks(t.Context(), host("x.test").locks())
	if err != nil || busy {
		t.Fatalf("after the matrix: busy=%v err=%v", busy, err)
	}
	release(t.Context())
}
