//go:build linux && metal

// adr: 476
package fcvm

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalResourceLinksReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on dedicated Linux KVM")
	}
	for _, replacement := range []string{"dummy", "veth", "address", "rename"} {
		t.Run(replacement, func(t *testing.T) {
			m := newTestManager(wire.ExecRunner{}, &fakeVMM{})
			j := openTestResourceJournal(t, t.TempDir())
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			l := journalTestLease(idLive, 0)
			if err := j.begin(l); err != nil {
				t.Fatal(err)
			}
			nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
			held := "grh" + uuid.NewString()[:8]
			foreignPeer := "grp" + uuid.NewString()[:8]
			run := func(argv ...string) {
				t.Helper()
				if err := m.run.Run(t.Context(), argv); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				for _, name := range []string{nc.VethHost, held, foreignPeer} {
					_ = m.run.Run(context.WithoutCancel(t.Context()), []string{"ip", "link", "del", name})
				}
				_ = m.run.Run(context.WithoutCancel(t.Context()), []string{"ip", "netns", "del", nc.Netns})
				leakcheck.AssertZero(t)
			})
			run("ip", "netns", "add", nc.Netns)
			ns, err := m.probeNamespace(nc.Netns)
			if err != nil || ns == nil {
				t.Fatal("capture real namespace", err)
			}
			m.rememberNamespace(nc.Netns, *ns)
			if err := m.runJournalIPSetup(t.Context(), nc, [][]string{{"ip", "link", "add", nc.VethHost, "type", "veth", "peer", "name", nc.VethPeer}}); err != nil {
				t.Fatal(err)
			}
			run("ip", "link", "set", nc.VethPeer, "netns", nc.Netns)
			original, err := m.checkOwnedLink(idLive, nc.VethHost)
			if err != nil || original == nil {
				t.Fatal("atomic address/index creation checkpoint", err)
			}
			if replacement == "address" {
				run("ip", "link", "set", nc.VethHost, "address", "02:00:00:00:00:aa")
			} else {
				run("ip", "link", "set", nc.VethHost, "name", held)
				switch replacement {
				case "dummy":
					run("ip", "link", "add", nc.VethHost, "address", "02:00:00:00:00:bb", "type", "dummy")
				case "veth":
					// An explicit address keeps udev's persistent-MAC policy
					// from changing this foreign fixture between observations.
					run("ip", "link", "add", nc.VethHost, "address", "02:00:00:00:00:bb", "type", "veth", "peer", "name", foreignPeer)
				}
			}
			foreign, err := m.probeLink(nc.VethHost, original.Index)
			if err != nil || foreign == nil {
				t.Fatal("replacement observation", err)
			}
			if err := m.teardownJournalNetwork(t.Context(), nc); err == nil {
				t.Fatal("foreign/renamed link authorized network teardown")
			}
			after, err := m.probeLink(nc.VethHost, original.Index)
			if err != nil || after == nil || *after != *foreign {
				t.Fatalf("replacement mutated: before=%+v after=%+v err=%v", foreign, after, err)
			}
			if afterNS, err := m.probeNamespace(nc.Netns); err != nil || afterNS == nil || !namespaceCheckpointMatches(*afterNS, *ns) {
				t.Fatal("namespace deletion preceded link guard")
			}
			if replacement == "address" {
				run("ip", "link", "set", nc.VethHost, "address", original.Address)
			} else {
				if replacement != "rename" {
					run("ip", "link", "del", nc.VethHost)
				}
				run("ip", "link", "set", held, "name", nc.VethHost)
			}
			if err := m.teardownJournalNetwork(t.Context(), nc); err != nil {
				t.Fatal("restored owner cleanup", err)
			}
			if err := j.forget(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}
