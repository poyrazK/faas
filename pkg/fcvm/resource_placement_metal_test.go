//go:build linux && metal

// adr: 475
// adr: 477
// adr: 479
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

func TestMetalResourcePlacementPreparedAlias(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on dedicated Linux KVM")
	}
	m, p := testPreparedPool(t, 1)
	m.preparedNetworks = nil
	j := openTestResourceJournal(t, t.TempDir())
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	m.preparedNetworks = p
	m.run = wire.ExecRunner{}
	p.move, p.removed = movePreparedNetns, preparedNetworkRemoved
	l, err := m.alloc.reserveNetwork("prepared-" + uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		_ = m.run.Run(ctx, []string{"ip", "link", "del", nc.VethHost})
		_ = m.run.Run(ctx, []string{"ip", "netns", "del", nc.Netns})
		_ = m.run.Run(ctx, []string{"ip", "netns", "del", "fc-" + idLive})
		leakcheck.AssertZero(t)
	})
	creator, err := resourcePlacementContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := j.beginPrepared(l, creator.BootID); err != nil {
		t.Fatal(err)
	}
	if err := j.addAsset(l.Instance, resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", nc.Netns), Namespace: creator}); err != nil {
		t.Fatal(err)
	}
	if err := m.run.Run(t.Context(), []string{"ip", "netns", "add", nc.Netns}); err != nil {
		t.Fatal(err)
	}
	a, err := resourceNetworkNamespaceAt(nc.Netns)
	if err != nil || a == nil {
		t.Fatalf("prepared nsfs observation: %v", err)
	}
	m.rememberNamespace(nc.Netns, *a)
	if err := j.checkpointAsset(l.Instance, a.Path, *a.File, a.Mount); err != nil {
		t.Fatal(err)
	}
	if err := m.runJournalIPSetup(t.Context(), nc, [][]string{{"ip", "link", "add", nc.VethHost, "type", "veth", "peer", "name", nc.VethPeer}}); err != nil {
		t.Fatal(err)
	}
	if err := m.run.Run(t.Context(), []string{"ip", "link", "set", nc.VethPeer, "netns", nc.Netns}); err != nil {
		t.Fatal(err)
	}
	p.ready = []preparedNetworkEntry{{lease: l, config: nc, created: time.Now()}}
	e := p.claim(idLive, preparedNetworkPolicy{})
	if e == nil {
		t.Fatal("real namespace alias transfer failed")
	}
	t.Cleanup(func() { p.discard(*e); leakcheck.AssertZero(t) })
	claimed := journalTestLease(idLive, e.lease.Slot)
	if err := m.journalLease(claimed); err != nil {
		t.Fatal(err)
	}
	if err := m.checkpointPreparedNamespace(e.config); err != nil {
		t.Fatal(err)
	}
	after, err := resourceNetworkNamespaceAt(e.config.Netns)
	if err != nil || after == nil || *a.File != *after.File || *a.Mount == *after.Mount {
		t.Fatal("alias move failed to preserve nsfs inode and change mount identity")
	}
	p.discard(*e)
	if len(p.retired) != 0 || m.LeasedCount() != 0 {
		t.Fatal("real prepared teardown did not retire owner")
	}
	if err := j.forget(claimed); err != nil {
		t.Fatal(err)
	}
}

func TestMetalResourcePlacementForeignJailMount(t *testing.T) {
	v, _, _, _ := metalAssetFixture(t)
	root, err := v.mkChroot(idLive)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "foreign-mount")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", target, "tmpfs", 0, "size=1m"); err != nil {
		t.Fatal(err)
	}
	mounted := true
	t.Cleanup(func() {
		if mounted {
			_ = unix.Unmount(target, 0)
		}
		if err := v.removeOwnedJail(idLive); err != nil {
			t.Errorf("jail fixture cleanup: %v", err)
		}
	})
	if err := v.removeOwnedJail(idLive); err == nil {
		t.Fatal("unrecorded nested mount was traversed")
	}
	if mount, err := resourceMountAt(target); err != nil || mount == nil {
		t.Fatal("foreign mount changed during jail cleanup")
	}
	if err := unix.Unmount(target, 0); err != nil {
		t.Fatal(err)
	}
	mounted = false
	if err := v.removeOwnedJail(idLive); err != nil {
		t.Fatal(err)
	}
}

func TestMetalResourcePlacementNamespaceReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on dedicated Linux KVM")
	}
	j := openTestResourceJournal(t, t.TempDir())
	m := newTestManager(wire.ExecRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	l := journalTestLease(idLive, 0)
	if err := j.begin(l); err != nil {
		t.Fatal(err)
	}
	name := l.Netns
	run := wire.ExecRunner{}
	if err := run.Run(t.Context(), []string{"ip", "netns", "add", name}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = run.Run(context.Background(), []string{"ip", "netns", "del", name})
		leakcheck.AssertZero(t)
	})
	a, err := resourceNetworkNamespaceAt(name)
	if err != nil || a == nil {
		t.Fatalf("nsfs checkpoint: %v", err)
	}
	m.rememberNamespace(name, *a)
	if err := m.checkpointPreparedNamespace(netns.Config{Instance: idLive, Netns: name}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/run/netns", name)
	original, err := os.Open(path) // Hold the inode to prevent immediate reuse.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = original.Close() }()
	for _, argv := range [][]string{{"ip", "netns", "del", name}, {"ip", "netns", "add", name}} {
		if err := run.Run(t.Context(), argv); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := resourceNetworkNamespaceAt(name)
	if err != nil || foreign == nil || namespaceCheckpointMatches(*a, *foreign) {
		t.Fatal("fixture did not replace namespace binding")
	}
	if err := m.checkOwnedNamespace(name); err == nil {
		t.Fatal("replaced namespace accepted")
	}
	if err := run.Run(t.Context(), []string{"ip", "netns", "del", name}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.checkOwnedNamespace(name); err == nil {
		t.Fatal("plain marker accepted as namespace")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := m.retireOwnedNamespace(netns.Config{Instance: idLive, Netns: name}); err != nil {
		t.Fatal(err)
	}
}
