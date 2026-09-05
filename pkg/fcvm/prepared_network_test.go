package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

func testPreparedPool(t *testing.T, capacity int) (*Manager, *preparedNetworkPool) {
	t.Helper()
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	p := &preparedNetworkPool{m: m, capacity: capacity, ctx: ctx, cancel: cancel,
		notify: make(chan struct{}, 1), move: func(string, string) error { return nil },
		removed: func(netns.Config) bool { return true }}
	m.preparedNetworks = p
	t.Cleanup(func() {
		for _, e := range append(p.ready, p.retired...) {
			p.discard(e)
		}
	})
	return m, p
}

func fillTestPreparedPool(t *testing.T, m *Manager, p *preparedNetworkPool, rate int) preparedNetworkPolicy {
	t.Helper()
	policy, ok := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: rate})
	if !ok {
		t.Fatal("default policy excluded")
	}
	p.observe(policy)
	p.fill()
	return policy
}

func TestPreparedNetworkClaimsAreUniqueAndNotAdmission(t *testing.T) {
	m, p := testPreparedPool(t, 3)
	policy := fillTestPreparedPool(t, m, p, 250)
	if len(p.ready) != 3 || m.LeasedCount() != 0 {
		t.Fatal("reservation charged as VM or wrong cache size")
	}
	var wg sync.WaitGroup
	claimed := make(chan *preparedNetworkEntry, 12)
	for i := range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); claimed <- p.claim(fmt.Sprintf("instance-%d", i), policy) }()
	}
	wg.Wait()
	close(claimed)
	slots := map[int]bool{}
	for e := range claimed {
		if e == nil {
			continue
		}
		if slots[e.lease.Slot] || e.config.Netns != "fc-"+e.lease.Instance || e.config.TapUID != e.lease.UID {
			t.Fatal("duplicate slot or inconsistent transferred identity")
		}
		slots[e.lease.Slot] = true
		if _, err := m.alloc.adoptNetwork(e.lease.Instance, "second-owner"); err == nil {
			t.Fatal("adopted twice")
		}
		if err := m.alloc.Release(e.lease.Instance); err != nil {
			t.Fatal(err)
		}
	}
	if len(slots) != 3 || len(p.ready) != 0 || m.LeasedCount() != 0 || len(m.alloc.reserved) != 0 {
		t.Fatal("claims leaked or reused a network")
	}
}

func TestPreparedNetworkDuplicateInstanceKeepsOriginalLease(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	policy := fillTestPreparedPool(t, m, p, 250)
	original, err := m.alloc.Acquire("same")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.alloc.Release("same") }()
	if p.claim("same", policy) != nil {
		t.Fatal("duplicate instance claimed prepared network")
	}
	if m.alloc.byInstance["same"] != original.Slot || len(m.alloc.reserved) != 0 {
		t.Fatal("original lease lost or reservation leaked")
	}
}

func TestPreparedNetworkPolicyAndExpiry(t *testing.T) {
	m, p := testPreparedPool(t, 2)
	policy := fillTestPreparedPool(t, m, p, 100)
	other := policy
	other.egressMbit = 250
	if p.claim("wrong-rate", other) != nil {
		t.Fatal("wrong rate matched")
	}
	for i := range p.ready {
		p.ready[i].created = time.Now().Add(-2 * preparedNetworkTTL)
	}
	if p.claim("expired", policy) != nil {
		t.Fatal("expired network claimed")
	}
	p.observed = time.Now().Add(-2 * preparedNetworkTTL)
	p.fill()
	if len(p.ready) != 0 || len(m.alloc.reserved) != 0 {
		t.Fatal("idle cache refreshed expired resources")
	}
	for _, req := range []WakeRequest{
		{Plan: "scale", ExportDir: "/builder"}, {Plan: "scale", StaticEgressIP: "1.2.3.4"},
		{Plan: "scale", EgressAllowlist: []string{"1.2.3.4/32"}}, {Plan: "invalid"},
	} {
		if _, ok := m.preparedPolicy(req); ok {
			t.Fatalf("unsupported policy eligible: %+v", req)
		}
	}
}

func TestPreparedNetworkChangedConfigRebuildsPolicy(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	policy := fillTestPreparedPool(t, m, p, 250)
	e := p.claim("changed", policy)
	if e == nil {
		t.Fatal("cache miss")
	}
	defer func() { _ = m.alloc.Release(e.lease.Instance) }()
	if hit, err := m.setupWakeNetwork(t.Context(), e.config, e); !hit || err != nil {
		t.Fatalf("same policy miss: %v", err)
	}
	nc := e.config
	nc.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}
	if hit, err := m.setupWakeNetwork(t.Context(), nc, e); hit || err != nil {
		t.Fatalf("changed policy not rebuilt: %v", err)
	}
	run := m.run.(*fakeRunner)
	if !run.ran("ip netns del fc-changed") || !run.ran("1.1.1.1/32") {
		t.Fatal("old policy survived rebuild")
	}
}

func TestPreparedNetworkClaimFailureFallsBack(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	fillTestPreparedPool(t, m, p, 250)
	p.move = func(string, string) error { return errors.New("bind failure") }
	lease, entry, err := m.acquireWakeNetwork(WakeRequest{Instance: "fallback", Plan: "scale", EgressMbit: 250})
	if err != nil || entry != nil {
		t.Fatalf("ordinary acquisition failed: %v", err)
	}
	defer func() { _ = m.alloc.Release(lease.Instance) }()
	if len(m.alloc.reserved) != 0 || m.LeasedCount() != 1 {
		t.Fatal("claim failure leaked a slot")
	}
}

func TestPreparedNetworkTeardownFailureRetainsSlot(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	fillTestPreparedPool(t, m, p, 250)
	p.removed = func(netns.Config) bool { return false }
	p.observed = time.Now().Add(-2 * preparedNetworkTTL)
	p.fill()
	if len(p.retired) != 1 || len(m.alloc.reserved) != 1 || len(p.ready) != 0 {
		t.Fatal("failed teardown released reserved identity")
	}
	p.removed = func(netns.Config) bool { return true }
	p.fill()
	if len(p.retired) != 0 || len(m.alloc.reserved) != 0 {
		t.Fatal("failed teardown was not retried")
	}
}

func TestPreparedNetworkShutdownDrainsWorker(t *testing.T) {
	m, p := testPreparedPool(t, 2)
	p.done = make(chan struct{})
	go p.run()
	policy, _ := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: 250})
	p.observe(policy)
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		n := len(p.ready)
		p.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not fill cache")
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.ClosePreparedNetworks(); err != nil {
		t.Fatal(err)
	}
	if len(m.alloc.reserved) != 0 || len(p.ready) != 0 {
		t.Fatal("shutdown leaked reserved resources")
	}
	if p.claim("late", policy) != nil {
		t.Fatal("claim after shutdown")
	}
}
