// adr: 402
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func resourceLinkFixture(t *testing.T) (*Manager, *ResourceJournal, *namespaceFixture, *Instance) {
	t.Helper()
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	j := openTestResourceJournal(t, t.TempDir())
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	f := installNamespaceFixture(m)
	inst, err := m.Wake(t.Context(), wakeReq(idLive, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Destroy(context.WithoutCancel(t.Context()), idLive); err != nil {
			t.Errorf("link fixture cleanup: %v", err)
		}
	})
	return m, j, f, inst
}

func TestResourceLinksRejectReplacementBeforeNamespaceDeletion(t *testing.T) {
	for _, field := range []string{"index", "address", "kind", "rename", "creator", "absent_creator", "unknown_owner"} {
		t.Run(field, func(t *testing.T) {
			m, j, f, inst := resourceLinkFixture(t)
			name := inst.Net.VethHost
			original := f.links[name]
			foreign := original
			context := m.linkContext
			owner, _ := m.linkOwner(name)
			switch field {
			case "index":
				foreign.Index++
			case "address":
				foreign.Address = "someone-else"
			case "kind":
				foreign.Kind = "dummy"
			case "rename":
				foreign.Name = "renamed"
				delete(f.links, name)
			case "creator", "absent_creator":
				m.linkContext = func() (*resourceMountIdentity, error) {
					return &resourceMountIdentity{BootID: idLive, Namespace: 2}, nil
				}
			case "unknown_owner":
				delete(m.resourceLinks, name)
			}
			f.links[foreign.Name] = foreign
			if field == "absent_creator" {
				delete(f.links, name)
			}
			before := cloneResourceAssets(j.records[idLive].Assets)
			if err := m.Destroy(t.Context(), idLive); err == nil || m.LeasedCount() != 1 {
				t.Fatalf("uncertain link released lease: %v", err)
			}
			if _, exists := f.bindings[inst.Net.Netns]; !exists || !reflect.DeepEqual(before, j.records[idLive].Assets) {
				t.Fatal("link preflight failure mutated namespace or journal")
			}
			if field != "absent_creator" && f.links[foreign.Name] != foreign {
				t.Fatal("foreign link mutated")
			}
			delete(f.links, foreign.Name)
			f.links[name] = original
			m.linkContext = context
			m.rememberLink(idLive, owner.asset)
		})
	}
}

func TestResourceLinksDurableOrderingAndFailures(t *testing.T) {
	for _, phase := range []string{"intent", "checkpoint", "retirement"} {
		t.Run(phase, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			j := openTestResourceJournal(t, t.TempDir())
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			f := installNamespaceFixture(m)
			injected := errors.New("veth journal fsync fixture")
			syncDir := j.directorySync
			j.directorySync = func() error {
				for _, a := range j.records[idLive].Assets {
					if a.Kind != "veth" {
						continue
					}
					_, created := f.links[a.Link.Name]
					if a.Link.Index == 0 && created {
						t.Fatal("creation happened before intent commit")
					}
					if (phase == "intent" && a.Link.Index == 0) || (phase == "checkpoint" && a.Link.Index != 0) {
						return injected
					}
				}
				return syncDir()
			}
			f.before = func(argv []string) {
				if len(argv) >= 4 && argv[0] == "ip" && argv[1] == "link" && argv[2] == "set" {
					r, _, _ := j.lookup(idLive)
					for _, a := range r.Assets {
						if a.Kind == "veth" && a.Link.Index == 0 {
							t.Fatal("link configured before checkpoint commit")
						}
					}
				}
			}
			inst, err := m.Wake(t.Context(), wakeReq(idLive, nil))
			f.before = nil
			if phase != "retirement" {
				if !errors.Is(err, injected) {
					t.Fatalf("failed commit acknowledged: %v", err)
				}
				if phase == "intent" && len(f.links) != 0 {
					t.Fatal("failed intent created a link")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				j.directorySync = func() error { return injected }
				if err := m.Destroy(t.Context(), idLive); !errors.Is(err, injected) || m.LeasedCount() != 1 {
					t.Fatalf("uncertain retirement released lease: %v", err)
				}
				if _, ok := m.linkOwner(inst.Net.VethHost); !ok {
					t.Fatal("uncertain retirement lost creator observation")
				}
			}
			j.directorySync = syncDir
			if err := m.Destroy(t.Context(), idLive); err != nil || m.LeasedCount() != 0 || len(f.links) != 0 {
				t.Fatalf("cleanup retry: %v", err)
			}
		})
	}
}

func TestResourceLinksPrivateReplacementAndUnpublishedAttachment(t *testing.T) {
	m, _, f, inst := resourceLinkFixture(t)
	nc := inst.Net
	nc.PrivateVethHost, nc.PrivateVethPeer = privateVethNames(inst.Lease.Slot)
	argv := []string{"ip", "link", "add", nc.PrivateVethHost, "type", "veth", "peer", "name", nc.PrivateVethPeer}
	if err := m.runJournalIPSetup(t.Context(), nc, [][]string{argv}); err != nil {
		t.Fatal(err)
	}
	// Simulate a failure before publishing attachment fields to live Config.
	original := f.links[nc.PrivateVethHost]
	foreign := original
	foreign.Address = "foreign"
	f.links[nc.PrivateVethHost] = foreign
	if err := m.Destroy(t.Context(), idLive); err == nil || m.LeasedCount() != 1 {
		t.Fatal("unpublished private attachment was omitted from cleanup fencing")
	}
	if _, ok := f.links[nc.VethHost]; !ok {
		t.Fatal("preflight mutated public link before rejecting private replacement")
	}
	f.links[nc.PrivateVethHost] = original
}

func TestResourceLinksLivePrivateReconciliation(t *testing.T) {
	m, j, f, inst := resourceLinkFixture(t)
	m.mu.Lock()
	inst.AccountID, inst.AppID = "acct-links", "app-links"
	m.mu.Unlock()
	cidrs := []netip.Prefix{netip.MustParsePrefix("10.90.0.0/24")}
	address := netip.MustParseAddr("10.90.0.2")
	if err := m.UpdatePrivateNetworkAttachment(t.Context(), inst.AppID, "pn-links", address, cidrs); err != nil {
		t.Fatal("live attachment", err)
	}
	name := inst.Net.PrivateVethHost
	original := f.links[name]
	foreign := original
	foreign.Address = "foreign"
	f.links[name] = foreign
	commands := 0
	f.before = func([]string) { commands++ }
	if err := m.UpdatePrivateNetworkAttachment(t.Context(), inst.AppID, "pn-links", address, cidrs); err == nil {
		t.Fatal("same-attachment fast path accepted a foreign link")
	}
	if err := m.UpdatePrivateNetwork(t.Context(), inst.AppID, nil); err == nil {
		t.Fatal("live detachment accepted a foreign link")
	}
	if commands != 0 || f.links[name] != foreign {
		t.Fatal("reconciliation mutated policy or foreign topology before fencing")
	}
	f.before = nil
	f.links[name] = original
	if err := m.UpdatePrivateNetworkAttachment(t.Context(), inst.AppID, "pn-replace", address, cidrs); err != nil {
		t.Fatal("guarded replacement", err)
	}
	if f.links[name].Address == original.Address {
		t.Fatal("replacement reused its predecessor's creation epoch")
	}
	if err := m.UpdatePrivateNetwork(t.Context(), inst.AppID, nil); err != nil {
		t.Fatal("guarded detachment", err)
	}
	r, _, _ := j.lookup(idLive)
	for _, a := range r.Assets {
		if a.Path == resourceLinkPath(name) {
			t.Fatal("detached private link retained a journal asset")
		}
	}
	if _, exists := f.links[name]; exists {
		t.Fatal("detached private link survived")
	}
}

func TestResourceLinksPreparedReplacementRetainsReservation(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	m.preparedNetworks = nil
	j := openTestResourceJournal(t, t.TempDir())
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	m.preparedNetworks = p
	f := installNamespaceFixture(m)
	policy := fillTestPreparedPool(t, m, p, 100)
	e := p.ready[0]
	original := f.links[e.config.VethHost]
	foreign := original
	foreign.Address = "foreign"
	f.links[e.config.VethHost] = foreign
	if p.claim(idLive, policy) != nil || len(p.retired) != 1 || len(m.alloc.reserved) != 1 {
		t.Fatal("prepared replacement was claimed or released")
	}
	if _, ok := f.bindings[e.config.Netns]; !ok || f.links[e.config.VethHost] != foreign {
		t.Fatal("prepared foreign topology mutated")
	}
	f.links[e.config.VethHost] = original
	p.retired = nil
	p.discard(e)
	if len(m.alloc.reserved) != 0 {
		t.Fatal("restored prepared topology did not clean up")
	}
}

func TestResourceLinksRecordValidationAndClone(t *testing.T) {
	m, j, _, inst := resourceLinkFixture(t)
	r, _, _ := j.lookup(idLive)
	if r.Version != 4 {
		t.Fatal("veth provenance did not upgrade the journal")
	}
	for _, a := range r.Assets {
		if a.Kind == "veth" {
			a.Link.Address = "tampered-copy"
		}
	}
	if _, err := m.checkOwnedLink(idLive, inst.Net.VethHost); err != nil {
		t.Fatal("journal lookup leaked mutable link identity")
	}
	r, _, _ = j.lookup(idLive)
	for _, invalid := range []string{"legacy", "networkless", "name", "epoch", "index", "extra"} {
		t.Run(invalid, func(t *testing.T) {
			bad := r
			bad.Assets = cloneResourceAssets(r.Assets)
			var a *resourceAsset
			for i := range bad.Assets {
				if bad.Assets[i].Kind == "veth" {
					a = &bad.Assets[i]
				}
			}
			switch invalid {
			case "legacy":
				bad.Version = 3
			case "networkless":
				bad.Lease.Networkless = true
			case "name":
				a.Link.Name = "foreign"
			case "epoch":
				a.Link.Address = "gregale:invalid"
			case "index":
				a.Link.Index = -1
			case "extra":
				a.File = &resourceFileIdentity{Inode: 1}
			}
			if err := bad.validate(); err == nil {
				t.Fatal("invalid veth record accepted")
			}
		})
	}
}
