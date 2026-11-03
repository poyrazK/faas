package fcvm

// adr: 20261103230000

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestStaticEgressUsesEveryLiveLeaseBeforeBootAndAfterTeardown(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	m.SetHostRenderer(&fakeHostRenderer{})
	const account, app = "acct-static-source", "app-static-source"
	ip := "203.0.113.42"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ip); err != nil {
		t.Fatal(err)
	}
	vmm.coldBootHook = func(lease Lease) {
		for _, rule := range netns.ActiveHostPolicyForRender().StaticEgressRules {
			if rule.PerVMHostIP == lease.HostIP && rule.CustomerIP.String() == ip {
				return
			}
		}
		t.Errorf("customer code started without static SNAT for lease %s", lease.HostIP)
	}
	var instances []*Instance
	for _, id := range []string{"static-source-one", "static-source-two"} {
		inst, err := m.Wake(t.Context(), WakeRequest{Instance: id, AccountID: account, AppID: app,
			StaticEgressIP: ip, BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
			VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanScale, EgressMbit: 10})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		instances = append(instances, inst)
	}
	rules := netns.ActiveHostPolicyForRender().StaticEgressRules
	if len(rules) != 2 || rules[0].PerVMHostIP == rules[1].PerVMHostIP {
		t.Fatalf("concurrent leases did not get distinct SNAT matches: %+v", rules)
	}
	for _, inst := range instances {
		found := false
		for _, rule := range rules {
			found = found || rule.PerVMHostIP == inst.Net.HostIP
		}
		if !found {
			t.Errorf("no SNAT match for namespace MASQUERADE source %s", inst.Net.HostIP)
		}
	}
	if err := m.Destroy(t.Context(), instances[0].Lease.Instance); err != nil {
		t.Fatal(err)
	}
	rules = netns.ActiveHostPolicyForRender().StaticEgressRules
	if len(rules) != 1 || rules[0].PerVMHostIP != instances[1].Net.HostIP {
		t.Fatalf("teardown removed a sibling's rule or retained a freed source: %+v", rules)
	}
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ""); err != nil {
		t.Fatal(err)
	}
	if rules := netns.ActiveHostPolicyForRender().StaticEgressRules; len(rules) != 0 {
		t.Fatalf("cleared pin retained SNAT rules: %+v", rules)
	}
}

func TestStaticEgressRenderFailurePreventsBootAndRetries(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	renderer := &fakeHostRenderer{renderErr: errors.New("nft reload failed")}
	m.SetHostRenderer(renderer)
	const account, app = "acct-static-failure", "app-static-failure"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, "203.0.113.42"); err == nil {
		t.Fatal("failed render was acknowledged")
	}
	request := WakeRequest{Instance: "static-failure", AccountID: account, AppID: app,
		StaticEgressIP: "203.0.113.42", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanScale, EgressMbit: 10}
	if _, err := m.Wake(t.Context(), request); err == nil || vmm.bootCount != 0 {
		t.Fatalf("boot proceeded without installed SNAT: err=%v boots=%d", err, vmm.bootCount)
	}
	if len(m.staticEgressStarting) != 0 || len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 0 {
		t.Fatal("failed wake retained a prospective source")
	}
	renderer.renderErr = nil
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, "203.0.113.42"); err != nil {
		t.Fatalf("same-pin retry did not repair host policy: %v", err)
	}
}

func TestStaticEgressGatewayRuleIsScopedAndReplaySafe(t *testing.T) {
	runner := &fakeRunner{}
	probe := &fakeCaptureRunner{}
	m := newTestManager(runner, &fakeVMM{}).WithCaptureRunner(probe)
	if err := m.ensureStaticEgressRouting(t.Context(), netns.TenantBridge); err != nil {
		t.Fatal(err)
	}
	if !runner.ran("priority 100 iif br-tenants fwmark 0x40000000/0x40000000 lookup main") {
		t.Fatalf("missing marked bridge-only routing exception: %v", runner.commands)
	}
	probe.listChainOutput = []byte("100: from all fwmark 0x40000000/0x40000000 iif br-tenants lookup main\n")
	before := len(runner.commands)
	if err := m.ensureStaticEgressRouting(t.Context(), netns.TenantBridge); err != nil || len(runner.commands) != before {
		t.Fatalf("existing routing exception was duplicated: %v", err)
	}
	probe.listChainErr = errors.New("ip rule probe failed")
	if err := m.ensureStaticEgressRouting(t.Context(), netns.TenantBridge); err == nil || !strings.Contains(err.Error(), "inspect static egress routing") {
		t.Fatalf("failed rule probe was ignored: %v", err)
	}
}

func TestStaticEgressPinSurvivesQuarantinedFork(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetHostRenderer(&fakeHostRenderer{})
	const account, app = "acct-static-fork", "app-static-fork"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, "203.0.113.42"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), WakeRequest{Instance: "static-fork", AccountID: account, AppID: app, Quarantine: true,
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128,
		Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "static-fork") })
	if ip := m.perAppStaticIP[app]; ip == nil || ip.String() != "203.0.113.42" {
		t.Fatalf("quarantined fork overwrote its parent's pin: %v", ip)
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 0 {
		t.Fatal("quarantined fork received a public SNAT source")
	}
}

func TestStaticEgressStageRemainsUnpinnedAcrossProductionUpdates(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetHostRenderer(&fakeHostRenderer{})
	const account, app = "acct-static-stage", "app-static-stage"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, "203.0.113.42"); err != nil {
		t.Fatal(err)
	}
	stage, err := m.Wake(t.Context(), WakeRequest{Instance: "static-stage", AccountID: account, AppID: app,
		StaticEgressExcluded: true, BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128,
		Plan: api.PlanScale, EgressMbit: 10, Snapshot: usableSnapshot()})
	if err != nil {
		t.Fatalf("stage wake was rejected after production pin: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "static-stage") })
	production, err := m.Wake(t.Context(), WakeRequest{Instance: "static-production", AccountID: account, AppID: app,
		StaticEgressIP: "203.0.113.42", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128,
		Plan: api.PlanScale, EgressMbit: 10, Snapshot: usableSnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "static-production") })
	for _, pin := range []string{"203.0.113.43", "", "203.0.113.42"} {
		if err := m.UpdateStaticEgressIP(t.Context(), account, app, pin); err != nil {
			t.Fatal(err)
		}
		rules := netns.ActiveHostPolicyForRender().StaticEgressRules
		if pin == "" {
			if len(rules) != 0 {
				t.Fatalf("clear retained rules: %v", rules)
			}
		} else if len(rules) != 1 || rules[0].PerVMHostIP != production.Lease.HostIP || rules[0].PerVMHostIP == stage.Lease.HostIP {
			t.Fatalf("pin %s included stage or omitted production: %v", pin, rules)
		}
	}
}

func TestPrivateNetworkWithdrawalRemovesSideLinkWithFirewallRules(t *testing.T) {
	runner := &fakeRunner{}
	m := newTestManager(runner, &fakeVMM{})
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "private-withdrawal", AccountID: "acct-private", AppID: "app-private",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128,
		Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot(), PrivateNetworkID: "private-net",
		PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{"10.42.0.0/24"},
		PrivateNetworkFirewallRules: []api.PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "tcp", Ports: []string{"443"}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "private-withdrawal") })
	link := inst.Net.PrivateVethHost
	if link == "" {
		t.Fatal("fixture has no private side-link")
	}
	if err := m.UpdatePrivateNetwork(t.Context(), inst.AppID, nil); err != nil {
		t.Fatal(err)
	}
	if !runner.ran("ip link del "+link) || inst.Net.PrivateVethHost != "" || inst.Net.PrivateNetworkAddress != (netip.Addr{}) {
		t.Fatalf("withdrawal retained its private link or address: %+v", inst.Net)
	}
}

type withdrawalBatchRunner struct {
	fakeInputRunner
	fail bool
}

func (r *withdrawalBatchRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if r.fail {
		return errors.New("nft transaction failed")
	}
	return r.fakeInputRunner.RunInput(ctx, argv, input)
}

func TestPrivateNetworkWithdrawalRetriesFailedTransactionBeforeRemovingLink(t *testing.T) {
	runner := &withdrawalBatchRunner{}
	m := newTestManager(runner, &fakeVMM{})
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "private-withdrawal-retry", AccountID: "acct-private", AppID: "app-private",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128,
		Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot(), PrivateNetworkID: "private-net",
		PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{"10.42.0.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "private-withdrawal-retry") })
	link := inst.Net.PrivateVethHost
	if link == "" {
		t.Fatal("fixture has no private side-link")
	}
	commandsBefore := len(runner.commands)
	runner.fail = true
	if err := m.UpdatePrivateNetwork(t.Context(), inst.AppID, nil); err == nil {
		t.Fatal("failed policy transaction was acknowledged")
	}
	for _, command := range runner.commands[commandsBefore:] {
		if strings.Join(command, " ") == "ip link del "+link {
			t.Fatal("failed transaction removed the link needed for a safe retry")
		}
	}
	if inst.Net.PrivateVethHost != link {
		t.Fatalf("failed transaction changed the private side-link: got=%q want=%q", inst.Net.PrivateVethHost, link)
	}
	runner.fail = false
	if err := m.UpdatePrivateNetwork(t.Context(), inst.AppID, nil); err != nil {
		t.Fatalf("withdrawal could not recover: %v", err)
	}
	if !runner.ran("ip link del "+link) || inst.Net.PrivateVethHost != "" {
		t.Fatal("successful retry left its private side-link attached")
	}
	batch := string(runner.input)
	if !strings.Contains(batch, "delete table ip faas") || !strings.Contains(batch, "add table ip faas") {
		t.Fatalf("withdrawal did not replace policy atomically: %s", batch)
	}
}
