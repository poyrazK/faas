// adr: 20261107180000
package edgedns_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/edgedns"
)

// R8-4: every compute node can write the registry, so a drain alone must not
// empty the records onto one node. Drained nodes whose edges still pass stay
// while withdrawing them would leave fewer than half of the healthy nodes; a
// drained node that fails its probe still goes.
func TestReconcilerRefusesAMassDrainOfHealthyNodes(t *testing.T) {
	addrC := netip.MustParseAddr("203.0.113.3")
	dns := newFakeDNS(addrA, addrB, addrC)
	f := newFixture(t, dns, false)
	nodes := func(bActive, cActive, cReady bool) []edgedns.Node {
		return []edgedns.Node{
			{Name: "a", Public: addrA, Active: true, Ready: true},
			{Name: "b", Public: addrB, Active: bActive, Ready: true},
			{Name: "c", Public: addrC, Active: cActive, Ready: cReady},
		}
	}
	if err := f.rec.Step(context.Background(), nodes(false, false, true)); err != nil {
		t.Fatal(err)
	}
	f.want(addrA, addrB, addrC)
	if v := testutil.ToFloat64(f.metrics.DrainRefusedForTest("proxied")); v != 2 {
		t.Fatalf("edge_dns_drain_refused_nodes = %v, want 2", v)
	}

	// One drained node of three is an ordinary drain.
	if err := f.rec.Step(context.Background(), nodes(false, true, true)); err != nil {
		t.Fatal(err)
	}
	f.want(addrA, addrC)
	if v := testutil.ToFloat64(f.metrics.DrainRefusedForTest("proxied")); v != 0 {
		t.Fatalf("edge_dns_drain_refused_nodes = %v after an honored drain, want 0", v)
	}

	// A drained node that stops passing goes on its probes.
	for range edgedns.DefaultFailAfter {
		if err := f.rec.Step(context.Background(), nodes(false, false, false)); err != nil {
			t.Fatal(err)
		}
	}
	f.want(addrA)
}
