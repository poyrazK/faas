package livekeys_test

// adr: 20261016151947 — a sealed live key or login opens only under the
// legacy namespace or the one bound to its own database, and the key and
// login namespaces never accept each other.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

func TestSealNamespacesBindToTheirDatabase(t *testing.T) {
	const db, other = "3f1c0b6e-9a2b-44d1-8c7e-0123456789ab", "0a1b2c3d-0000-4000-8000-000000000001"
	cases := []struct {
		name, namespace, database string
		key, login                bool
	}{
		{"legacy key", livekeys.SealNamespace, db, true, false},
		{"legacy login", livekeys.LoginSealNamespace, db, false, true},
		{"key bound here", livekeys.KeySealNamespaceFor(db), db, true, false},
		{"login bound here", livekeys.LoginSealNamespaceFor(db), db, false, true},
		{"key bound elsewhere", livekeys.KeySealNamespaceFor(other), db, false, false},
		{"login bound elsewhere", livekeys.LoginSealNamespaceFor(other), db, false, false},
		{"bound key for an unnamed database", livekeys.KeySealNamespaceFor(""), "", false, false},
		{"bound login for an unnamed database", livekeys.LoginSealNamespaceFor(""), "", false, false},
		{"another domain", "secrets", db, false, false},
	}
	for _, tc := range cases {
		if got := livekeys.KeyNamespaceOpens(tc.namespace, tc.database); got != tc.key {
			t.Errorf("%s: KeyNamespaceOpens(%q, %q) = %v, want %v", tc.name, tc.namespace, tc.database, got, tc.key)
		}
		if got := livekeys.LoginNamespaceOpens(tc.namespace, tc.database); got != tc.login {
			t.Errorf("%s: LoginNamespaceOpens(%q, %q) = %v, want %v", tc.name, tc.namespace, tc.database, got, tc.login)
		}
	}
}
