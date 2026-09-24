// adr: 053
// adr: 149 — prepared-network reuse preserves exact identity and policy matching.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
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
	original := map[string]bool{}
	for _, entry := range p.ready {
		original[entry.lease.Instance] = true
	}
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
	p.fill()
	if len(p.ready) != 2 || len(m.alloc.reserved) != 2 {
		t.Fatal("observed policy was not kept warm after entry expiry")
	}
	for _, entry := range p.ready {
		if original[entry.lease.Instance] {
			t.Fatal("expired prepared network was reused instead of refreshed")
		}
	}
	for _, req := range []WakeRequest{
		{Plan: "scale", ExportDir: "/builder"}, {Plan: "scale", StaticEgressIP: "1.2.3.4"},
		{Plan: "scale", EgressAllowlist: []string{"1.2.3.4/32"}}, {Plan: "scale", Port: -1},
		{Plan: "scale", Port: 65536}, {Plan: "invalid"},
	} {
		if _, ok := m.preparedPolicy(req); ok {
			t.Fatalf("unsupported policy eligible: %+v", req)
		}
	}
	// The guest port is not part of the policy: every app port shares one pool.
	want, _ := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: 250})
	for _, port := range []int{0, netns.AppPort, 3000, 8000, 65535} {
		if got, ok := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: 250, Port: port}); !ok || got != want {
			t.Fatalf("port %d: policy=%+v ok=%v, want %+v", port, got, ok, want)
		}
	}
}

func TestPreparedNetworkRefreshesOldSpareBeforeHardExpiry(t *testing.T) {
	m, p := testPreparedPool(t, 3)
	policy := fillTestPreparedPool(t, m, p, 100)
	old := p.ready[0]
	young := p.ready[1].lease.Instance
	// An unused spare can be older than the other entries after repeated
	// bursts smaller than capacity.
	p.ready[0].created = time.Now().Add(-3 * preparedNetworkTTL / 4)
	p.observe(policy)
	p.fill()
	if len(p.ready) != 3 || len(m.alloc.reserved) != 3 || m.LeasedCount() != 0 {
		t.Fatal("refresh changed cache capacity or admitted a guest")
	}
	keptYoung := false
	for _, entry := range p.ready {
		if entry.lease.Instance == old.lease.Instance {
			t.Fatal("aging spare was not replaced before hard expiry")
		}
		keptYoung = keptYoung || entry.lease.Instance == young
	}
	if !keptYoung {
		t.Fatal("refresh rebuilt a young entry unnecessarily")
	}
	// Each entry remains usable exactly once in the next full burst.
	for i := range 3 {
		entry := p.claim(fmt.Sprintf("refreshed-%d", i), policy)
		if entry == nil {
			t.Fatal("full burst missed after refreshing the old spare")
		}
		p.discard(*entry)
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

func TestPreparedNetworkDefaultPortMatches(t *testing.T) {
	for _, ports := range [][2]int{{0, 0}, {0, netns.AppPort}, {netns.AppPort, 0}, {netns.AppPort, netns.AppPort}} {
		t.Run(fmt.Sprintf("%d-to-%d", ports[0], ports[1]), func(t *testing.T) {
			m, p := testPreparedPool(t, 1)
			policy := fillTestPreparedPool(t, m, p, 250)
			e := p.claim("default-port", policy)
			if e == nil {
				t.Fatal("cache miss")
			}
			defer p.discard(*e)
			e.config.GuestAppPort = ports[0]
			nc := e.config
			nc.GuestAppPort = ports[1]
			// Prove the two representations install exactly the same rules;
			// matching them must not broaden the prepared policy.
			if !reflect.DeepEqual(e.config.SetupCommands(), nc.SetupCommands()) ||
				!reflect.DeepEqual(e.config.NftCommands(), nc.NftCommands()) ||
				!reflect.DeepEqual(e.config.TcCommands(), nc.TcCommands()) {
				t.Fatal("default-port representations render different networks")
			}
			run := m.run.(*fakeRunner)
			before := len(run.commands)
			if hit, err := m.setupWakeNetwork(t.Context(), nc, e); !hit || err != nil {
				t.Fatalf("equivalent default port rebuilt network: hit=%v err=%v", hit, err)
			}
			if len(run.commands) != before {
				t.Fatal("cache hit executed network commands")
			}
			if e.config.GuestAppPort != ports[0] || nc.GuestAppPort != ports[1] {
				t.Fatal("comparison mutated caller configuration")
			}
		})
	}
}

func TestPreparedNetworkWakeKeepsNetworkForAnyPort(t *testing.T) {
	for _, port := range []int{0, netns.AppPort, 3000, 8000} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			m, p := testPreparedPool(t, 1)
			fillTestPreparedPool(t, m, p, 250)
			run := m.run.(*fakeRunner)
			setupBefore, teardownBefore := run.setupCount, run.teardownCount
			instance := fmt.Sprintf("wake-default-%d", port)
			t.Cleanup(func() {
				if err := m.Destroy(context.Background(), instance); err != nil {
					t.Error(err)
				}
			})
			inst, err := m.Wake(t.Context(), WakeRequest{
				Instance: instance, BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
				VcpuCount: 2, MemSizeMiB: 128, Plan: "scale", EgressMbit: 250,
				Port: port, Snapshot: usableSnapshot(),
			})
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if inst.Method != WakeRestore || inst.Port != port {
				t.Fatalf("wake changed method or port: method=%v port=%d", inst.Method, inst.Port)
			}
			if run.setupCount != setupBefore || run.teardownCount != teardownBefore {
				t.Fatalf("wake rebuilt prepared network: setup=%d->%d teardown=%d->%d",
					setupBefore, run.setupCount, teardownBefore, run.teardownCount)
			}
			if m.LeasedCount() != 1 || len(p.ready) != 0 || len(m.alloc.reserved) != 0 {
				t.Fatal("wake did not transfer the prepared reservation exactly once")
			}
			custom := port != 0 && port != netns.AppPort
			if retargeted := run.ran("flush chain ip faas prerouting"); retargeted != custom {
				t.Fatalf("port %d: DNAT retarget ran=%v", port, retargeted)
			}
			if custom && !run.ran(fmt.Sprintf("dnat to %s:%d", netns.GuestIP, port)) {
				t.Fatalf("port %d: DNAT not retargeted to the guest port", port)
			}
		})
	}
}

// A claimed namespace prepared for :8080 serves any valid guest port: only
// the DNAT target changes, replaced in one nft transaction, and nothing else
// runs. (ADR-149 amendment, 2026-09-24.)
func TestPreparedNetworkRetargetsGuestPortInOneTransaction(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	prepared := netns.NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	for n, port := range []int{3000, 8000, 65535} {
		requested := prepared
		requested.GuestAppPort = port
		hit, err := m.setupWakeNetwork(t.Context(), requested, &preparedNetworkEntry{config: prepared})
		if err != nil || !hit {
			t.Fatalf("port %d: hit=%v err=%v, want a retargeted hit", port, hit, err)
		}
		if run.inputRuns != n+1 || strings.Join(run.inputArgv, " ") != "ip netns exec fc-i nft -f -" {
			t.Fatalf("port %d: nft runs=%d argv=%v, want one `nft -f -` transaction", port, run.inputRuns, run.inputArgv)
		}
		want := fmt.Sprintf("flush chain ip faas prerouting\nadd rule ip faas prerouting iifname vp0 tcp dport 8080 dnat to %s:%d\n", netns.GuestIP, port)
		if string(run.input) != want {
			t.Fatalf("port %d: transaction\n%s\nwant\n%s", port, run.input, want)
		}
		if len(run.commands) != 0 {
			t.Fatalf("retarget ran commands outside the transaction: %v", run.commands)
		}
	}
}

// If the retarget fails the namespace is rebuilt through ordinary setup, as
// any other mismatch is, before a VMM starts.
func TestPreparedNetworkRetargetFailureRebuilds(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	policy := fillTestPreparedPool(t, m, p, 250)
	e := p.claim("retarget-fails", policy)
	if e == nil {
		t.Fatal("cache miss")
	}
	defer p.discard(*e)
	run := m.run.(*fakeRunner)
	run.failOn = "flush chain ip faas prerouting"
	nc := e.config
	nc.GuestAppPort = 3000
	setupBefore := run.setupCount
	hit, err := m.setupWakeNetwork(t.Context(), nc, e)
	if hit || err != nil {
		t.Fatalf("hit=%v err=%v, want a rebuild", hit, err)
	}
	if run.setupCount == setupBefore || !run.ran(fmt.Sprintf("dnat to %s:3000", netns.GuestIP)) {
		t.Fatal("failed retarget did not rebuild the network for the requested port")
	}
}

// Only the guest port may differ, and only to a valid port: every other
// identity or policy change still rebuilds.
func TestPreparedNetworkRetargetOnlyForPort(t *testing.T) {
	prepared := netns.NewConfig("same", "fc-same", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	for _, tc := range []struct {
		name   string
		change func(*netns.Config)
		want   bool
	}{
		{"custom_port", func(c *netns.Config) { c.GuestAppPort = 3000 }, true},
		{"max_port", func(c *netns.Config) { c.GuestAppPort = 65535 }, true},
		{"negative_port", func(c *netns.Config) { c.GuestAppPort = -1 }, false},
		{"overflow_port", func(c *netns.Config) { c.GuestAppPort = 65536 }, false},
		{"port_and_instance", func(c *netns.Config) { c.GuestAppPort, c.Instance = 3000, "other" }, false},
		{"port_and_tap_owner", func(c *netns.Config) { c.GuestAppPort = 3000; c.TapUID++ }, false},
		{"port_and_egress_rate", func(c *netns.Config) { c.GuestAppPort = 3000; c.EgressMbit++ }, false},
		{"port_and_conntrack_cap", func(c *netns.Config) { c.GuestAppPort = 3000; c.ConntrackCap++ }, false},
		{"port_and_allowlist", func(c *netns.Config) {
			c.GuestAppPort = 3000
			c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}
		}, false},
		{"port_and_private_network", func(c *netns.Config) {
			c.GuestAppPort = 3000
			c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
		}, false},
		{"port_and_egress_circuit", func(c *netns.Config) { c.GuestAppPort = 3000; c.EgressCircuitEnabled = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := prepared
			tc.change(&requested)
			if got := preparedNetworkDiffersOnlyInPort(prepared, requested); got != tc.want {
				t.Fatalf("differs only in port = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPreparedNetworkDefaultPortDoesNotHidePolicyChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*netns.Config)
	}{
		{"custom_port", func(c *netns.Config) { c.GuestAppPort = 3000 }},
		{"negative_port", func(c *netns.Config) { c.GuestAppPort = -1 }},
		{"overflow_port", func(c *netns.Config) { c.GuestAppPort = 65536 }},
		{"instance", func(c *netns.Config) { c.Instance = "other" }},
		{"namespace", func(c *netns.Config) { c.Netns = "fc-other" }},
		{"tap_owner", func(c *netns.Config) { c.TapUID++ }},
		{"host_ip", func(c *netns.Config) { c.HostIP = netip.MustParseAddr("10.100.2.3") }},
		{"bridge", func(c *netns.Config) { c.HostBridgeIP = netip.MustParseAddr("10.101.0.1") }},
		{"egress_rate", func(c *netns.Config) { c.EgressMbit++ }},
		{"conntrack_cap", func(c *netns.Config) { c.ConntrackCap++ }},
		{"egress_allowlist", func(c *netns.Config) {
			c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}
		}},
		{"private_network", func(c *netns.Config) {
			c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
		}},
		{"operator_exceptions", func(c *netns.Config) {
			c.OperatorExceptions = []netip.Prefix{netip.MustParsePrefix("10.43.0.0/24")}
		}},
		{"egress_circuit", func(c *netns.Config) { c.EgressCircuitEnabled = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared := netns.NewConfig("same", "fc-same", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
			requested := prepared
			requested.GuestAppPort = netns.AppPort
			tc.change(&requested)
			if preparedNetworkConfigMatches(prepared, requested) || preparedNetworkConfigMatches(requested, prepared) {
				t.Fatal("default-port normalization hid a configuration change")
			}
		})
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
	p.desired = nil
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
