//go:build linux || darwin

// adr: 493 — network effects and deletion retain their original launch authority.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func nativeNetworkFixture(t *testing.T) (*Manager, *JailerVMM, *Instance) {
	t.Helper()
	m, v, _ := nativeManagerFixture(t)
	l := leaseForSlot("network-scope", 0)
	l.Plan = api.PlanHobby
	if err := v.prepareNativeLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{Lease: l, Net: nativeLeaseNetwork(l), AppID: "network-app", AccountID: "network-account", Plan: l.Plan}
	inst.Net.DNSGated = true
	inst.Net.EgressCircuitEnabled = true
	if err := m.stampNativeInstanceGeneration(inst); err != nil {
		t.Fatal(err)
	}
	m.live[l.Instance] = inst
	return m, v, inst
}

func retireNativeNetworkOwner(t *testing.T, v *JailerVMM, instance string) nativeLaunchRecord {
	t.Helper()
	j := v.nativeRecovery.journal
	owner, err := j.revoke(t.Context(), instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.confirmExit(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	owner, err = j.read(instance)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func replaceNativeNetworkOwner(t *testing.T, v *JailerVMM, inst *Instance) nativeLaunchRecord {
	t.Helper()
	owner := retireNativeNetworkOwner(t, v, inst.Lease.Instance)
	if err := v.nativeRecovery.journal.confirmResourcesRemoved(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	owner, err := v.nativeRecovery.journal.replace(t.Context(), inst.Lease, owner.Generation, true)
	if err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.remember(owner)
	return owner
}

// The gate and subprocess are real; this backend models Linux cgroups. This is
// portable protocol evidence, not dedicated native network acceptance.
func nativeNetworkProductionDispatch(t *testing.T, m *Manager, v *JailerVMM) string {
	t.Helper()
	_, marker := nativeHelperFixtureCommand(t)
	v.nativeRecovery.helper = os.Args[0]
	v.nativeRecovery.startTime = func(int) (uint64, error) { return 1234, nil }
	m.vmm = v
	dir := t.TempDir()
	if err := os.Symlink(os.Args[0], filepath.Join(dir, "ip")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func TestNativeNetworkBatchesAndCaptureUseGatedOwnerWithoutLegacyRunner(t *testing.T) {
	m, v, inst := nativeNetworkFixture(t)
	nativeNetworkProductionDispatch(t, m, v)
	ctx, err := m.nativeInstanceNetworkContext(t.Context(), inst.Lease.Instance, inst.nativeGeneration)
	if err != nil {
		t.Fatal(err)
	}
	inputMarker := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("GREGALE_NATIVE_HELPER_INPUT_MARKER", inputMarker)
	nft := []string{"ip", "netns", "exec", inst.Net.Netns, "nft", "flush", "set", "ip", "faas", "egress_ports"}
	if err := m.runNftCommands(ctx, inst.Net.Netns, [][]string{nft}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(inputMarker); err != nil || string(data) != "flush set ip faas egress_ports\n" {
		t.Fatalf("nft stdin=%q err=%v", data, err)
	}
	cmds := [][]string{{"ip", "link", "set", inst.Net.VethHost, "up"}, {"ip", "link", "set", inst.Net.PrivateVethHost, "up"}}
	if err := m.runIPSetupCommands(ctx, cmds); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(inputMarker); err != nil || string(data) != strings.Join(cmds[0][1:], " ")+"\n"+strings.Join(cmds[1][1:], " ")+"\n" {
		t.Fatalf("ip stdin=%q err=%v", data, err)
	}
	t.Setenv("GREGALE_NATIVE_HELPER_INPUT_MARKER", "")
	t.Setenv("GREGALE_NATIVE_HELPER_OUTPUT", "iifname \"tap0\" ip daddr { 1.2.3.0/24 } accept # handle 42\n")
	cap, err := m.networkCaptureRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if handle, err := listChainHandles(ctx, cap, inst.Net.Netns, "ip", "faas", "forward"); err != nil || handle != 42 {
		t.Fatalf("captured handle=%d err=%v", handle, err)
	}
	owner, err := v.nativeRecovery.journal.read(inst.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: v.nativeRecovery.helperGroups}
	frames, err := j.records(owner)
	if err != nil || len(frames) != 3 {
		t.Fatalf("gated frames=%+v err=%v", frames, err)
	}
	for _, frame := range frames {
		if frame.OwnerGeneration != inst.nativeGeneration || frame.Purpose != nativeHostHelperEffect || !frame.Launch.ResourcesRemoved {
			t.Fatalf("command lost original ownership: %+v", frame)
		}
	}
	if len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("native commands reached the legacy runner")
	}
}

func TestNativeNetworkMissingAndSupersededScopeCannotLaunch(t *testing.T) {
	m, v, inst := nativeNetworkFixture(t)
	marker := nativeNetworkProductionDispatch(t, m, v)
	cmds := inst.Net.EgressPortsUpdateCommands()
	if err := m.runNftCommands(t.Context(), inst.Net.Netns, cmds); err == nil {
		t.Fatal("native command borrowed an unscoped legacy runner")
	}
	ctx, err := m.nativeInstanceNetworkContext(t.Context(), inst.Lease.Instance, inst.nativeGeneration)
	if err != nil {
		t.Fatal(err)
	}
	replaceNativeNetworkOwner(t, v, inst)
	if err := m.runNftCommands(ctx, inst.Net.Netns, cmds); err == nil {
		t.Fatal("captured scope acquired a replacement generation")
	}
	if _, err := m.nativeInstanceNetworkContext(t.Context(), inst.Lease.Instance, inst.nativeGeneration); err == nil {
		t.Fatal("stale instance obtained fresh command authority")
	}
	if _, err := m.nativeInstanceNetworkContext(t.Context(), inst.Lease.Instance, ""); err == nil {
		t.Fatal("retained instance without a launch identity obtained authority")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected scope executed a subprocess")
	}
	if v.nativeRecovery.helperGroups.(*nativeHelperGroupsFixture).creates != 0 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("rejected scope reached a command producer")
	}
}

func TestNativeNetworkCleanupDeletesOnlyOriginalRetiredLease(t *testing.T) {
	m, v, inst := nativeNetworkFixture(t)
	nativeNetworkProductionDispatch(t, m, v)
	if _, _, err := m.nativeCleanupNetworkContext(t.Context(), inst.Lease); err == nil {
		t.Fatal("cleanup acquired an active VM")
	}
	owner := retireNativeNetworkOwner(t, v, inst.Lease.Instance)
	v.nativeRecovery.mu.Lock()
	delete(v.nativeRecovery.owned, inst.Lease.Instance) // recovered cleanup has no local producer
	v.nativeRecovery.mu.Unlock()
	ctx, skip, err := m.nativeCleanupNetworkContext(t.Context(), inst.Lease)
	if err != nil || skip {
		t.Fatalf("retired cleanup skip=%v err=%v", skip, err)
	}
	for _, argv := range nativeLeaseNetwork(inst.Lease).TeardownCommands() {
		if err := m.runNetworkCommand(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	groups := v.nativeRecovery.helperGroups.(*nativeHelperGroupsFixture)
	created := groups.creates
	for _, argv := range [][]string{
		{"ip", "netns", "add", inst.Net.Netns},
		{"ip", "netns", "del", "fc-another-instance"},
		{"ip", "link", "del", "br-tenants"},
		{"ip", "netns", "exec", inst.Net.Netns, "ip", "link", "del", "tap0"},
		{"sh", "-c", "ip netns del " + inst.Net.Netns},
	} {
		if err := m.runNetworkCommand(ctx, argv); err == nil {
			t.Fatalf("cleanup allowed broader command: %v", argv)
		}
	}
	runner, err := m.networkCommandRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.(InputRunner).RunInput(ctx, []string{"ip", "netns", "del", inst.Net.Netns}, []byte{}); err == nil {
		t.Fatal("cleanup accepted additional stdin authority")
	}
	if groups.creates != created {
		t.Fatal("invalid cleanup forked a helper")
	}
	j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: groups}
	frames, err := j.records(owner)
	if err != nil || len(frames) != len(nativeLeaseNetwork(inst.Lease).TeardownCommands()) {
		t.Fatalf("cleanup frames=%+v err=%v", frames, err)
	}
	for _, frame := range frames {
		if frame.Purpose != nativeHostHelperNetworkCleanup || !frame.Launch.ResourcesRemoved {
			t.Fatalf("cleanup lost restricted ownership: %+v", frame)
		}
	}
	if err := v.nativeRecovery.journal.confirmResourcesRemoved(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, skip, err := m.nativeCleanupNetworkContext(t.Context(), inst.Lease); err != nil || !skip {
		t.Fatalf("duplicate cleanup skip=%v err=%v", skip, err)
	}
	if err := m.runNetworkCommand(ctx, nativeLeaseNetwork(inst.Lease).TeardownCommands()[0]); err == nil || groups.creates != created {
		t.Fatal("acknowledged cleanup created another producer")
	}
}

func TestNativeNetworkUnfinishedHelperStopsFurtherEffects(t *testing.T) {
	m, v, inst := nativeNetworkFixture(t)
	nativeNetworkProductionDispatch(t, m, v)
	ctx, err := m.nativeInstanceNetworkContext(t.Context(), inst.Lease.Instance, inst.nativeGeneration)
	if err != nil {
		t.Fatal(err)
	}
	groups := v.nativeRecovery.helperGroups.(*nativeHelperGroupsFixture)
	cause := errors.New("uncertain group retirement")
	groups.retireErr = cause
	argv := []string{"ip", "link", "set", inst.Net.VethHost, "up"}
	if err := m.runNetworkCommand(ctx, argv); !errors.Is(err, cause) {
		t.Fatalf("first helper=%v", err)
	}
	if err := m.runNetworkCommand(ctx, argv); err == nil || groups.creates != 1 {
		t.Fatal("unfinished helper allowed the next effect")
	}
	owner := retireNativeNetworkOwner(t, v, inst.Lease.Instance)
	cleanupCtx, _, err := m.nativeCleanupNetworkContext(t.Context(), inst.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.runNetworkCommand(cleanupCtx, nativeLeaseNetwork(inst.Lease).TeardownCommands()[0]); err == nil || groups.creates != 1 {
		t.Fatal("unfinished effect allowed overlapping cleanup")
	}
	if err := v.nativeRecovery.journal.confirmResourcesRemoved(t.Context(), owner); err == nil {
		t.Fatal("unfinished helper released original resource ownership")
	}
	groups.retireErr = nil
	j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: groups}
	if err := j.retireAll(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := m.runNetworkCommand(cleanupCtx, nativeLeaseNetwork(inst.Lease).TeardownCommands()[0]); err != nil {
		t.Fatal(err)
	}
}

func TestNativeNetworkCleanupUncertaintyRetainsAllocatorUntilRetry(t *testing.T) {
	m, v, inst := nativeNetworkFixture(t)
	nativeNetworkProductionDispatch(t, m, v)
	if err := m.alloc.reserveRecovered([]Lease{inst.Lease}); err != nil {
		t.Fatal(err)
	}
	m.nativeRecovered = map[string][]Lease{inst.Lease.Instance: {inst.Lease}}
	groups := v.nativeRecovery.helperGroups.(*nativeHelperGroupsFixture)
	cause := errors.New("cleanup group retirement uncertain")
	groups.retireErr = cause
	if err := m.cleanup(t.Context(), inst.Lease, inst.Net, nil); err == nil {
		t.Fatal("uncertain cleanup acknowledged complete retirement")
	}
	owner, err := v.nativeRecovery.journal.read(inst.Lease.Instance)
	if err != nil || !owner.Revoked || !owner.ExitConfirmed || owner.ResourcesRemoved || m.alloc.InUse() != 1 {
		t.Fatalf("cleanup lost holding: owner=%+v leases=%d err=%v", owner, m.alloc.InUse(), err)
	}
	if groups.creates != 1 {
		t.Fatal("uncertain cleanup launched subsequent deletion helpers")
	}
	groups.retireErr = nil
	if err := m.cleanup(t.Context(), inst.Lease, inst.Net, nil); err != nil {
		t.Fatal(err)
	}
	owner, err = v.nativeRecovery.journal.read(inst.Lease.Instance)
	if err != nil || !owner.ResourcesRemoved || m.alloc.InUse() != 0 {
		t.Fatalf("confirmed retry failed to release holding: owner=%+v leases=%d err=%v", owner, m.alloc.InUse(), err)
	}
	created := groups.creates
	if err := m.cleanup(t.Context(), inst.Lease, inst.Net, nil); err != nil || groups.creates != created {
		t.Fatalf("duplicate completed cleanup forked: creates=%d err=%v", groups.creates, err)
	}
}

func TestNativeNetworkSetupAndMarkerRefuseChangedOwnership(t *testing.T) {
	m, v, inst := nativeNetworkFixture(t)
	nc := inst.Net
	nc.VethHost = "unowned-host-interface"
	if err := m.setupNetwork(t.Context(), nc); err == nil || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("mismatched config reached network effects")
	}
	ctx, err := m.nativeInstanceNetworkContext(t.Context(), inst.Lease.Instance, inst.nativeGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.removeScopedStaleNetnsMarker(ctx, "unowned-netns"); err == nil {
		t.Fatal("file fallback accepted another namespace")
	}
	replaceNativeNetworkOwner(t, v, inst)
	if err := m.removeScopedStaleNetnsMarker(ctx, inst.Net.Netns); err == nil {
		t.Fatal("stale file fallback borrowed replacement ownership")
	}
}

func TestNativeNetworkPoliciesRejectStaleLiveGeneration(t *testing.T) {
	for _, operation := range []string{"allowlist", "ports", "private", "attachment", "circuit", "dns"} {
		t.Run(operation, func(t *testing.T) {
			m, v, inst := nativeNetworkFixture(t)
			replaceNativeNetworkOwner(t, v, inst)
			var err error
			switch operation {
			case "allowlist":
				err = m.UpdateEgressAllowlist(t.Context(), inst.AppID, []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")})
			case "ports":
				err = m.UpdateEgressPorts(t.Context(), inst.AppID, []uint16{8443})
			case "private":
				err = m.UpdatePrivateNetwork(t.Context(), inst.AppID, []netip.Prefix{netip.MustParsePrefix("10.20.0.0/24")})
			case "attachment":
				err = m.UpdatePrivateNetworkAttachment(t.Context(), inst.AppID, "team", netip.MustParseAddr("10.20.0.2"), []netip.Prefix{netip.MustParsePrefix("10.20.0.0/24")})
			case "circuit":
				err = m.UpdateEgressCircuit(t.Context(), inst.AppID, []netns.EgressCircuitTarget{{}})
			case "dns":
				err = m.AllowResolvedEgress(t.Context(), inst.Net.HostIP, []netip.Addr{netip.MustParseAddr("1.2.3.4")}, time.Minute)
			}
			if err == nil || !strings.Contains(err.Error(), "generation") || len(m.run.(*fakeRunner).commands) != 0 {
				t.Fatalf("stale %s effect err=%v", operation, err)
			}
		})
	}
}

func TestNativeNetworkLatePolicyCacheDoesNotAlterReplacement(t *testing.T) {
	m, _, inst := nativeNetworkFixture(t)
	replacement := &Instance{Lease: inst.Lease, Net: inst.Net, AppID: inst.AppID, Plan: inst.Plan}
	fixture := m.vmm.(*recoveryVMMFixture)
	fixture.command = func(context.Context, []string, []byte) ([]byte, error) {
		m.mu.Lock()
		m.live[inst.Lease.Instance] = replacement
		m.mu.Unlock()
		return nil, nil
	}
	if err := m.UpdateEgressPorts(t.Context(), inst.AppID, []uint16{8443}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(replacement.Net.EgressPorts, inst.Net.EgressPorts) {
		t.Fatal("late cache publication altered replacement instance")
	}
}

func TestNativeNetworkAttachmentFinalFanoutRetainsOriginalTargets(t *testing.T) {
	m, _, inst := nativeNetworkFixture(t)
	replacement := &Instance{Lease: inst.Lease, Net: inst.Net, AppID: inst.AppID, Plan: inst.Plan, nativeGeneration: inst.nativeGeneration}
	fixture := m.vmm.(*recoveryVMMFixture)
	fixture.command = func(context.Context, []string, []byte) ([]byte, error) {
		m.mu.Lock()
		m.live[inst.Lease.Instance] = replacement
		m.mu.Unlock()
		return nil, nil
	}
	err := m.UpdatePrivateNetworkAttachment(t.Context(), inst.AppID, "team", netip.MustParseAddr("10.20.0.2"), []netip.Prefix{netip.MustParsePrefix("10.20.0.0/24")})
	if err == nil || !strings.Contains(err.Error(), "original live targets changed") {
		t.Fatalf("attachment borrowed replacement targets: %v", err)
	}
	if len(replacement.Net.PrivateNetworkCIDRs) != 0 || replacement.Net.PrivateNetworkBridge != inst.Net.PrivateNetworkBridge {
		t.Fatal("late attachment published policy to replacement instance")
	}
}

func TestNativeNetworkHelperPurposeCompatibilityIsStrict(t *testing.T) {
	j, owner, _ := nativeHelperJournalFixture(t)
	frame := preparedNativeHelperFrame(t, j, owner)
	encoded, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var old nativeHostHelperRecord
	if err := json.Unmarshal(encoded, &old); err != nil || old.Purpose != "" || old.validate(owner) != nil {
		t.Fatalf("old effect shape=%+v err=%v", old, err)
	}
	old.Purpose = nativeHostHelperNetworkCleanup
	if err := json.Unmarshal(encoded, &old); err != nil || old.Purpose != "" || old.validate(owner) != nil {
		t.Fatalf("old effect shape inherited prior cleanup authority: %+v err=%v", old, err)
	}
	frame.Purpose = nativeHostHelperNetworkCleanup
	if err := frame.validate(owner); err == nil {
		t.Fatal("cleanup frame accepted an active VM")
	}
	for _, suffix := range []string{`,"purpose":null}`, `,"purpose":"effect","purpose":"network_cleanup"}`, `,"purpose":"unknown"}`} {
		data := append(slices.Clone(encoded[:len(encoded)-1]), []byte(suffix)...)
		var decoded nativeHostHelperRecord
		if err := json.Unmarshal(data, &decoded); err == nil && decoded.validate(owner) == nil {
			t.Fatalf("invalid helper purpose accepted: %s", data)
		}
	}
	if err := j.retire(t.Context(), owner, frame.Launch.Generation); err != nil {
		t.Fatal(err)
	}
}
