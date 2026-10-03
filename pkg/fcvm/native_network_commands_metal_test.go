//go:build linux && metal

// adr: 493 — physical network helpers retain frozen VM ownership and bounded cleanup.
package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// The outer test starts a private mount/network namespace on the dedicated
// native KVM host. Its fixtures cannot alter the node's bridge or /run/netns.
// This exercises prepared-VM network authority; it supplies no guest receipt.
func TestMetalNativeNetworkHelperProtocol(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires the dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires the dedicated native KVM acceptance host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if os.Getenv("GREGALE_NATIVE_NETWORK_ACCEPTANCE_CHILD") != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--net", "--propagation", "private", os.Args[0], "-test.run=^TestMetalNativeNetworkHelperProtocol$", "-test.timeout=90s", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_NETWORK_ACCEPTANCE_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated native network acceptance: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	run := wire.ExecRunner{}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run.Run(ctx, []string{"mount", "-t", "tmpfs", "tmpfs", "/run/netns"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"umount", "/run/netns"}) })
	for _, argv := range [][]string{
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	v := newMetalVMM(t, 10*time.Second)
	v.chrootBase = t.TempDir()
	v.WithNativeProcessRecovery()
	r := v.nativeRecovery
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.daemonLock != nil {
			_ = r.daemonLock.Close()
		}
	})
	legacy := &fakeRunner{failOn: "ip"}
	m := NewManager(legacy, v, Paths{}, "1.7.0", nil, nil)
	l := leaseForSlot("native-network-protocol", 0)
	l.Plan = api.PlanHobby
	if err := v.prepareNativeLease(ctx, l); err != nil {
		t.Fatal(err)
	}
	nc := nativeLeaseNetwork(l)
	nc.TapUID, nc.EgressMbit = l.UID, 100
	applyTenantEgressPolicy(&nc, l.Plan, nil)
	inst := &Instance{Lease: l, Net: nc, AppID: "native-network-app", Plan: l.Plan}
	if err := m.stampNativeInstanceGeneration(inst); err != nil {
		t.Fatal(err)
	}
	m.live[l.Instance] = inst
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := nativeMetalNetworkCleanup(cleanupCtx, m, v, l); err != nil {
			t.Error("native network fixture retirement:", err)
		}
	})
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatal(err)
	}
	assertBatchNetwork(t, nc)
	if err := m.UpdateEgressPorts(ctx, inst.AppID, []uint16{8443}); err != nil {
		t.Fatal(err)
	}
	originalCtx, err := m.nativeInstanceNetworkContext(ctx, l.Instance, inst.nativeGeneration)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := m.networkCaptureRunner(originalCtx)
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{"ip", "netns", "exec", nc.Netns, "nft", "list", "set", "ip", "faas", "egress_ports"}
	if out, err := cap.RunCapture(originalCtx, argv); err != nil || !strings.Contains(string(out), "8443") {
		t.Fatalf("native nft update/capture: %v %s", err, out)
	}
	owner, err := r.journal.read(l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	helpers := nativeHostHelperJournal{owner: r.journal, groups: r.helperGroups}
	frames, err := helpers.records(owner)
	if err != nil || len(frames) == 0 {
		t.Fatalf("native network frames=%+v err=%v", frames, err)
	}
	for _, frame := range frames {
		if frame.OwnerGeneration != inst.nativeGeneration || frame.Group.Inode == 0 || frame.Group.Device == 0 || !frame.Launch.ResourcesRemoved {
			t.Fatalf("network command has no physical retirement proof: %+v", frame)
		}
	}
	if err := helpers.requireRemoved(owner); err != nil {
		t.Fatal(err)
	}
	if err := nativeMetalNetworkCleanup(ctx, m, v, l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join("/run/netns", nc.Netns)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("namespace survived owned cleanup: %v", err)
	}
	if err := run.Run(ctx, []string{"ip", "link", "show", netns.TenantBridge}); err != nil {
		t.Fatal("owned cleanup removed the unrelated bridge:", err)
	}
	if err := v.prepareNativeLease(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := m.runNftCommands(originalCtx, nc.Netns, nc.EgressPortsUpdateCommands()); err == nil {
		t.Fatal("old scope patched the replacement network")
	}
	assertBatchNetwork(t, nc)
	if len(legacy.commands) != 0 {
		t.Fatal("native network used the legacy command runner")
	}
}

func nativeMetalNetworkCleanup(ctx context.Context, m *Manager, v *JailerVMM, l Lease) error {
	j := v.nativeRecovery.journal
	owner, err := j.read(l.Instance)
	if err != nil {
		return err
	}
	if owner.ResourcesRemoved {
		return v.nativeResourcesRemoved(l, nativeLeaseNetwork(l)) //nolint:contextcheck // Durable receipt verification does not inherit cancellable command authority.
	}
	owner, err = j.revoke(ctx, l.Instance)
	if err != nil {
		return err
	}
	helpers := nativeHostHelperJournal{owner: j, groups: v.nativeRecovery.helperGroups}
	if err := helpers.retireAll(ctx, owner); err != nil {
		return err
	}
	// This prepared owner has never started a jailer or Firecracker.
	if owner.Authorized || owner.PID != 0 {
		return errors.New("network fixture unexpectedly acquired a VM process")
	}
	if err := j.confirmExit(ctx, owner); err != nil {
		return err
	}
	cleanupCtx, _, err := m.nativeCleanupNetworkContext(ctx, l)
	if err != nil {
		return err
	}
	for _, argv := range nativeLeaseNetwork(l).TeardownCommands() {
		// Absence is valid for a partially created fixture. The physical
		// resource check below distinguishes that from uncertain deletion.
		_ = m.runNetworkCommand(cleanupCtx, argv)
	}
	return v.confirmNativeCleanup(ctx, l, nativeLeaseNetwork(l))
}
