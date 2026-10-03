// adr: 460 — retain exact-policy spares within the existing global capacity.
// spec: §6.3
package fcvm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

func TestPreparedNetworkAlternatingPoliciesRetainClaimableSpares(t *testing.T) {
	for _, capacity := range []int{2, 3} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			m, p := testPreparedPool(t, capacity)
			a := fillTestPreparedPool(t, m, p, 100)
			b := fillTestPreparedPool(t, m, p, 250)
			for i := range 12 {
				policy := a
				if i%2 != 0 {
					policy = b
				}
				entry := p.claim(fmt.Sprintf("alternating-%d", i), policy)
				if entry == nil {
					t.Fatalf("request %d missed a retained exact-policy spare", i)
				}
				if entry.policy != policy {
					t.Fatal("mismatched policy claimed")
				}
				p.discard(*entry)
				p.observe(policy)
				p.fill()
				if len(p.ready) != capacity || len(m.alloc.reserved) != capacity || m.LeasedCount() != 0 {
					t.Fatal("policy sharing changed global network-only capacity")
				}
			}
		})
	}
}

func TestPreparedNetworkAlternatingPlansKeepExactConnectionPolicy(t *testing.T) {
	m, p := testPreparedPool(t, 2)
	free, _ := m.preparedPolicy(WakeRequest{Plan: "free", EgressMbit: 100})
	scale, _ := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: 100})
	if free == scale {
		t.Fatal("fixture must differ in plan connection policy despite equal egress rate")
	}
	p.observe(free)
	p.fill()
	p.observe(scale)
	p.fill()
	for i, policy := range []preparedNetworkPolicy{free, scale} {
		entry := p.claim(fmt.Sprintf("plan-policy-%d", i), policy)
		if entry == nil || entry.policy != policy {
			t.Fatal("shared rate lost the exact plan-policy spare")
		}
		if entry.config.EgressConnRate != policy.egressConnRate || entry.config.EgressDestConnRate != policy.egressDestConnRate {
			t.Fatal("claim used another plan's connection limits")
		}
		p.discard(*entry)
	}
}

func TestPreparedNetworkPolicyChangeReplacesOnlyOldestSpare(t *testing.T) {
	m, p := testPreparedPool(t, 3)
	a := fillTestPreparedPool(t, m, p, 100)
	oldest := p.ready[1].lease.Instance
	p.ready[1].created = time.Now().Add(-time.Second)
	run := m.run.(*fakeRunner)
	setupBefore := preparedNamespaceSetups(run)
	b := fillTestPreparedPool(t, m, p, 250)
	counts := map[preparedNetworkPolicy]int{}
	for _, entry := range p.ready {
		counts[entry.policy]++
		if entry.lease.Instance == oldest {
			t.Fatal("oldest spare survived replacement")
		}
	}
	if counts[a] != 2 || counts[b] != 1 || preparedNamespaceSetups(run) != setupBefore+1 {
		t.Fatalf("replacement rebuilt the pool: policies=%v setup delta=%d", counts, preparedNamespaceSetups(run)-setupBefore)
	}
	p.observe(b)
	p.fill()
	if preparedNamespaceSetups(run) != setupBefore+1 {
		t.Fatal("already represented target caused more churn")
	}
}

func preparedNamespaceSetups(run *fakeRunner) int {
	run.mu.Lock()
	defer run.mu.Unlock()
	count := 0
	for _, argv := range run.commands {
		if len(argv) >= 3 && argv[0] == "ip" && argv[1] == "netns" && argv[2] == "add" {
			count++
		}
	}
	return count
}

func TestPreparedNetworkOneSlotStillTracksLatestPolicy(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	a := fillTestPreparedPool(t, m, p, 100)
	b := fillTestPreparedPool(t, m, p, 250)
	if len(p.ready) != 1 || p.ready[0].policy != b || len(m.alloc.reserved) != 1 {
		t.Fatal("one-slot pool did not replace its previous policy")
	}
	if p.claim("previous-policy", a) != nil {
		t.Fatal("wrong policy claimed a one-slot spare")
	}
}

type observeDuringPreparedSetupRunner struct {
	Runner
	once    sync.Once
	observe func()
}

func (r *observeDuringPreparedSetupRunner) Run(ctx context.Context, argv []string) error {
	r.once.Do(r.observe)
	return r.Runner.Run(ctx, argv)
}

func TestPreparedNetworkPolicyChangeDuringSetupKeepsCompletedSpare(t *testing.T) {
	m, p := testPreparedPool(t, 2)
	a, _ := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: 100})
	b, _ := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: 250})
	m.run = &observeDuringPreparedSetupRunner{Runner: m.run, observe: func() { p.observe(b) }}
	p.observe(a)
	p.fill()
	counts := map[preparedNetworkPolicy]int{}
	for _, entry := range p.ready {
		counts[entry.policy]++
	}
	if counts[a] != 1 || counts[b] != 1 || len(m.alloc.reserved) != 2 {
		t.Fatalf("completed eligible preparation discarded after policy change: %v", counts)
	}
}

func TestPreparedNetworkPolicyReplacementRetainsFailedTeardownSlot(t *testing.T) {
	m, p := testPreparedPool(t, 3)
	a := fillTestPreparedPool(t, m, p, 100)
	p.removed = func(netns.Config) bool { return false }
	b := fillTestPreparedPool(t, m, p, 250)
	if len(p.ready) != 2 || len(p.retired) != 1 || len(m.alloc.reserved) != 3 {
		t.Fatal("failed replacement teardown changed the global reserved-slot bound")
	}
	for _, entry := range p.ready {
		if entry.policy != a {
			t.Fatal("replacement prepared before its slot was released")
		}
	}
	if p.claim("blocked-new-policy", b) != nil {
		t.Fatal("new policy claimed after failed teardown")
	}
	p.removed = func(netns.Config) bool { return true }
	p.fill()
	counts := map[preparedNetworkPolicy]int{}
	for _, entry := range p.ready {
		counts[entry.policy]++
	}
	if len(p.retired) != 0 || len(m.alloc.reserved) != 3 || counts[a] != 2 || counts[b] != 1 {
		t.Fatal("retry did not reclaim the same bounded slot for the target policy")
	}
}
