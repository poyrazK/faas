package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"path/filepath"
	"testing"
)

const adoptionTestSpare = "prepared-" + idOther

// newPreparedAdoptionFixture is the fixture guest woken on a claimed
// prepared network (ADR-149), as production's default prepared-network cache
// leaves most guests: the spare's record was transferred to the guest and
// keeps the spare's storage key and creator boot.
func newPreparedAdoptionFixture(t *testing.T) *adoptionFixture {
	t.Helper()
	f := newAdoptionFixture(t)
	f.journal = openTestResourceJournal(t, filepath.Join(f.root, "journal-prepared"))
	ctx := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840}
	checkpoint := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840, MountID: 99}
	link := &resourceLinkIdentity{Index: 17, Name: f.lease.VethHost, Kind: "veth", Address: "02:00:00:00:00:03"}
	veth := resourceAsset{Kind: "veth", Path: resourceLinkPath(f.lease.VethHost), Namespace: ctx, Link: link}
	spare := leaseForSlot(adoptionTestSpare, f.lease.Slot)
	f.must(f.journal.beginPrepared(spare, idOther))
	f.must(f.journal.addAsset(spare.Instance, resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", spare.Netns), Namespace: ctx, File: &f.nsFile, Mount: checkpoint}))
	f.must(f.journal.addAsset(spare.Instance, veth))
	f.must(f.journal.transferPrepared(spare.Instance, idLive))
	// The claim moved the binding to the guest's name; identities are the spare's.
	ns := resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", f.lease.Netns), Namespace: ctx, File: &f.nsFile, Mount: checkpoint}
	f.must(f.journal.adoptPrepared(f.lease, ns, veth))
	f.must(f.journal.recordProcess(f.lease, f.process))
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "jail", Path: filepath.Dir(f.jailRoot), Namespace: ctx}))
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "jail", Path: f.jailRoot, Namespace: ctx}))
	return f
}

// A guest on a claimed prepared network is adopted, keeps its prepared
// identity, and retires the spare-keyed record through the ordinary teardown.
func TestAdoptRestartSurvivorsAdoptsAPreparedNetworkGuest(t *testing.T) {
	f := newPreparedAdoptionFixture(t)
	m, vmm := adoptionManager(t, f, f.block())
	rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Adopted != 1 || len(rep.Refused) != 0 {
		t.Fatalf("adoption = %+v", rep)
	}
	if !m.HasInstanceOwnership(idLive) || m.isRestartQuarantined(idLive) {
		t.Fatal("adopted guest is still quarantined or unowned")
	}
	r := f.record()
	if r.Prepared == nil || r.storageIdentity() != adoptionTestSpare || r.Prepared.Target != idLive || r.Version != resourceAdoptionVersion {
		t.Fatalf("adopted record lost its prepared identity: %+v", r)
	}
	for _, a := range r.Assets {
		if a.Kind == "netns" && (a.Mount == nil || a.Mount.Namespace != 4026532999 || a.Path != filepath.Join("/run/netns", f.lease.Netns)) {
			t.Fatalf("namespace checkpoint not re-anchored: %+v", a)
		}
	}
	if err := m.Destroy(t.Context(), idLive); err != nil {
		t.Fatalf("destroy the adopted guest: %v", err)
	}
	if records, err := f.journal.snapshot(); err != nil || len(records) != 0 {
		t.Fatalf("records after teardown = %+v, %v", records, err)
	}
	if l, err := m.alloc.Acquire("after"); err != nil || l.Slot != f.lease.Slot {
		t.Fatalf("slot not freed after teardown: %+v, %v", l, err)
	}
}

// Spares are never adopted: the classifier skips them and the scope check
// refuses one that reaches it.
func TestAdoptionScopeRefusesAnUnclaimedSpare(t *testing.T) {
	spare := resourceJournalRecord{Lease: leaseForSlot(adoptionTestSpare, 3), Prepared: &resourcePreparedNetwork{Source: adoptionTestSpare, BootID: idOther}}
	spare.Adoption = &resourceAdoption{}
	if reason, _ := adoptionScope(func(string) string { return "" }, false, spare); reason != adoptionRefusedPrepared {
		t.Fatalf("spare scope = %q, want %q", reason, adoptionRefusedPrepared)
	}
}

// A production fork (ADR-732) is refused: its quarantine is not rebuilt.
func TestAdoptionScopeRefusesAProductionFork(t *testing.T) {
	fork := resourceJournalRecord{Lease: leaseForSlot(idLive, 3), Adoption: &resourceAdoption{}}
	fork.Lease.Quarantine = true
	if reason, _ := adoptionScope(func(string) string { return "" }, false, fork); reason != adoptionRefusedProductionFork {
		t.Fatalf("fork scope = %q, want %q", reason, adoptionRefusedProductionFork)
	}
}
