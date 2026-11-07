// adr: 20261103163954
package edgecert_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/edgecert"
)

// With two sets of names, a newest certificate the edge cannot open for one
// set (sealed to an identity it does not hold yet) keeps that set's served
// certificate while the other set's renewal loads: a bad row never takes a
// name's certificate away.
func TestServedKeepsASetWhoseNewestCannotOpen(t *testing.T) {
	ca, id := newFakeCA(t), newIdentity(t)
	alias := []string{"*.gregale.app"}
	store := &liveStore{rows: []edgecert.Stored{stored(t, ca, wildcard, id, 1), stored(t, ca, alias, id, 2)}}
	s := newServed(t, store, id)
	if _, err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	aliasBefore, err := serverName(s, "app.gregale.app")
	if err != nil {
		t.Fatal(err)
	}
	// Renewals land: the wildcard's opens, the alias's is sealed elsewhere.
	store.rows = []edgecert.Stored{stored(t, ca, wildcard, id, 3), stored(t, ca, alias, newIdentity(t), 4)}
	changed, err := s.Load(t.Context())
	if !changed || err == nil {
		t.Fatalf("Load = changed %v, err %v; want the wildcard renewal served and the unopenable row reported", changed, err)
	}
	aliasAfter, err := serverName(s, "app.gregale.app")
	if err != nil {
		t.Fatalf("the alias names lost their certificate: %v", err)
	}
	if aliasAfter.Leaf.SerialNumber.Cmp(aliasBefore.Leaf.SerialNumber) != 0 {
		t.Fatal("the alias set should keep the certificate it served")
	}
	if _, err := serverName(s, "app.gregale.dev"); err != nil {
		t.Fatalf("the wildcard renewal is not served: %v", err)
	}
}
