// adr: 373 — DNS-gated egress; 20261026150000 — bound managed database egress.
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// A guest's workload runs before publication. A DNS answer it receives then
// must open its egress: a Data API that connects to its database at boot used
// to time out because the lookup found no network owner.
func TestBootingGuestOwnsItsDNSAnswers(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	database := netip.MustParseAddr("198.51.100.30")
	var source netip.Addr
	var bootOwner ServiceCallerIdentity
	var bootErr error
	_, err := m.WakeWithNetworkReady(t.Context(), WakeRequest{
		Instance: "boot-a", AppID: "app-boot", AccountID: "account", DeploymentID: "deployment",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby, EgressMbit: 100, Snapshot: usableSnapshot(),
	}, func(WakeNetworkReady) {
		m.mu.Lock()
		owner := m.bootingEgress["boot-a"]
		_, live := m.live["boot-a"]
		m.mu.Unlock()
		if owner == nil || live {
			bootErr = errors.New("no booting network owner before the guest starts")
			return
		}
		source = owner.Net.HostIP
		var matched bool
		bootOwner, matched = m.ResolveServiceCaller(source)
		if !matched {
			bootErr = errors.New("booting guest's address did not resolve")
			return
		}
		bootErr = m.AllowResolvedEgress(t.Context(), source, bootOwner, []netip.Addr{database}, time.Minute, 5432)
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "boot-a") })
	if bootErr != nil {
		t.Fatalf("grant during boot: %v", bootErr)
	}
	if bootOwner.AppID != "app-boot" || bootOwner.AccountID != "account" || bootOwner.DeploymentID != "deployment" || bootOwner.NetworkGeneration == "" {
		t.Fatalf("booting identity = %+v, want the wake's app identity", bootOwner)
	}
	if !strings.Contains(string(run.input), "add element ip faas egress_db { 198.51.100.30 . 5432 timeout ") {
		t.Fatalf("database pair not granted during boot:\n%s", run.input)
	}

	m.mu.Lock()
	inst, booting := m.live["boot-a"], len(m.bootingEgress)
	var generation string
	var pairs int
	if inst != nil {
		generation, pairs = inst.resolvedEgressGeneration, len(inst.databaseEgress)
	}
	m.mu.Unlock()
	if inst == nil || booting != 0 {
		t.Fatalf("publication: live %v, booting owners left %d", inst != nil, booting)
	}
	if generation != bootOwner.NetworkGeneration || pairs != 1 {
		t.Fatalf("published instance generation %q pairs %d, want the boot owner's %q and its grant", generation, pairs, bootOwner.NetworkGeneration)
	}
	// An answer whose owner was captured during boot still applies after
	// publication: the generation carried over.
	if err := m.AllowResolvedEgress(t.Context(), source, bootOwner, []netip.Addr{netip.MustParseAddr("198.51.100.31")}, time.Minute, 5432); err != nil {
		t.Fatalf("grant captured during boot, applied after publication: %v", err)
	}
	if after, _ := m.ResolveServiceCaller(source); after.NetworkGeneration != bootOwner.NetworkGeneration {
		t.Fatalf("live generation %q, want %q", after.NetworkGeneration, bootOwner.NetworkGeneration)
	}
}

// A wake that fails leaves no booting owner behind to authenticate a recycled
// address.
func TestFailedWakeDropsItsBootingOwner(t *testing.T) {
	m := newTestManager(&fakeInputRunner{}, &fakeVMM{bootErr: errors.New("boom")})
	var registered bool
	_, err := m.WakeWithNetworkReady(t.Context(), WakeRequest{
		Instance: "boot-fail", AppID: "app-boot", AccountID: "account", DeploymentID: "deployment",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby, EgressMbit: 100,
	}, func(WakeNetworkReady) {
		m.mu.Lock()
		registered = m.bootingEgress["boot-fail"] != nil
		m.mu.Unlock()
	})
	if err == nil {
		t.Fatal("Wake succeeded with a failing boot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !registered {
		t.Fatal("the booting owner was never registered")
	}
	if len(m.bootingEgress) != 0 {
		t.Fatalf("booting owners after a failed wake: %v", m.bootingEgress)
	}
}

func TestBootingEgressOwnerSkipsSlotsWithoutAnIdentity(t *testing.T) {
	nc := newTestManager(&fakeInputRunner{}, &fakeVMM{})
	for _, test := range []struct {
		name          string
		lease         Lease
		req           WakeRequest
		qualification bool
	}{
		{"execution", Lease{Instance: "x"}, WakeRequest{ExecutionOnly: true}, false},
		{"qualification", Lease{Instance: "x"}, WakeRequest{}, true},
		{"networkless", Lease{Instance: "x", Networkless: true}, WakeRequest{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			nc.registerBootingEgressOwner(test.lease, netns.Config{Netns: "ns-x"}, test.req, test.qualification)
			nc.mu.Lock()
			defer nc.mu.Unlock()
			if len(nc.bootingEgress) != 0 {
				t.Fatalf("registered %v", nc.bootingEgress)
			}
		})
	}
}

// A booting managed probe resolves without the app-task flag, so gatewayd
// cannot authenticate its service calls from the task lease before
// publication; its DNS grants still carry the app identity.
func TestBootingAppTaskIsNotAServiceCaller(t *testing.T) {
	m := newTestManager(&fakeInputRunner{}, &fakeVMM{})
	ip := netip.MustParseAddr("10.100.0.9")
	m.registerBootingEgressOwner(Lease{Instance: "probe", HostIP: ip}, netns.Config{Netns: "ns-probe", HostIP: ip},
		WakeRequest{AppID: "app", AccountID: "account", DeploymentID: "deployment", AppTaskOnly: true}, false)
	identity, matched := m.ResolveServiceCaller(ip)
	if !matched || identity.AppTask || identity.AppID != "app" {
		t.Fatalf("booting probe identity = %+v, %v", identity, matched)
	}
}
