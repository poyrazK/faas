package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateJudgesTheRunningVMMDsInventory(t *testing.T) {
	f := newAdoptionFixture(t)
	f.must(f.journal.recordAdoption(f.lease, f.process, f.block()))
	items, err := f.classifier().inventory(t.Context(), f.journal)
	if err != nil || len(items) != 1 || !items[0].ProofOK {
		t.Fatalf("inventory = %+v, %v", items, err)
	}
	sameJail := func(string) string { return f.jailRoot }
	if v := CandidateAdoptionVerdicts(items, sameJail, false); len(v) != 1 || !v[0].Adoptable || v[0].AppID != "app-1" {
		t.Fatalf("candidate verdicts = %+v", v)
	}
	// An app task needs no drain: the candidate would kill it, as a drain would.
	task := f.block()
	task.Kind = adoptionKindAppTask
	f.must(f.journal.recordAdoption(f.lease, f.process, task))
	taskItems, err := f.classifier().inventory(t.Context(), f.journal)
	if err != nil || len(taskItems) != 1 {
		t.Fatalf("inventory = %+v, %v", taskItems, err)
	}
	if v := CandidateAdoptionVerdicts(taskItems, sameJail, false); len(v) != 1 || !v[0].Adoptable || v[0].Resolved != AdoptionResolvedKilled {
		t.Fatalf("app task verdicts = %+v", v)
	}

	for _, c := range []struct {
		name   string
		item   func(AdoptionInventoryRecord) AdoptionInventoryRecord
		jail   func(string) string
		reason string
	}{
		{name: "a newer release's record", item: func(i AdoptionInventoryRecord) AdoptionInventoryRecord {
			i.Record = bytes.Replace(i.Record, []byte(`{"version"`), []byte(`{"future_field":1,"version"`), 1)
			return i
		}, reason: adoptionRefusedUndecodable},
		{name: "a different Firecracker", jail: func(string) string { return "/srv/fc/jail/firecracker-v2/x/root" }, reason: adoptionRefusedFCChanged},
		{name: "the live proof failed", item: func(i AdoptionInventoryRecord) AdoptionInventoryRecord {
			i.ProofOK, i.ProofReason, i.ProofDetail = false, adoptionRefusedProcessAbsent, "pid exited"
			return i
		}, reason: adoptionRefusedProcessAbsent},
		{name: "no proof given", item: func(i AdoptionInventoryRecord) AdoptionInventoryRecord {
			i.ProofOK, i.ProofReason = false, ""
			return i
		}, reason: adoptionRefusedUnproven},
	} {
		t.Run(c.name, func(t *testing.T) {
			item, jail := items[0], sameJail
			if c.item != nil {
				item = c.item(item)
			}
			if c.jail != nil {
				jail = c.jail
			}
			v := CandidateAdoptionVerdicts([]AdoptionInventoryRecord{item}, jail, false)
			if len(v) != 1 || v[0].Adoptable || v[0].Reason != c.reason || v[0].Instance != idLive {
				t.Fatalf("verdict = %+v, want refused %q", v, c.reason)
			}
		})
	}
}

// A record whose guest process is gone is nothing to adopt or lose, so it does
// not force a drain, with or without an adoption block. Anything not proven
// gone, an older live guest among them, still refuses.
func TestCandidateTreatsAGoneGuestAsNothingToAdopt(t *testing.T) {
	f := newAdoptionFixture(t)
	sameJail := func(string) string { return f.jailRoot }
	judge := func(c adoptionClassifier) (AdoptionInventoryRecord, AdoptionVerdict, AdoptionVerdict) {
		t.Helper()
		items, err := c.inventory(t.Context(), f.journal)
		if err != nil || len(items) != 1 {
			t.Fatalf("inventory = %+v, %v", items, err)
		}
		v := CandidateAdoptionVerdicts(items, sameJail, false)
		records, err := f.journal.snapshot()
		if err != nil || len(records) != 1 {
			t.Fatalf("journal = %+v, %v", records, err)
		}
		return items[0], v[0], c.classify(t.Context(), records[0])
	}

	// The fixture's record has no adoption block and a live process.
	item, cand, start := judge(f.classifier())
	if item.ProofReason != adoptionRefusedNoRecord || cand.Gone || cand.Adoptable || start.Gone || start.Reason != adoptionRefusedNoRecord {
		t.Fatalf("live guest without a block: item %+v, candidate %+v, startup %+v", item, cand, start)
	}

	// Recorded on another kernel boot.
	other := f.classifier()
	other.bootID = idLive
	item, cand, start = judge(other)
	if item.ProofReason != adoptionRefusedOtherBoot || !cand.Gone || !start.Gone || start.Reason != adoptionRefusedOtherBoot {
		t.Fatalf("other boot: item %+v, candidate %+v, startup %+v", item, cand, start)
	}

	// Its process exited.
	if err := os.RemoveAll(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID))); err != nil {
		t.Fatal(err)
	}
	item, cand, start = judge(f.classifier())
	if item.ProofReason != adoptionRefusedProcessAbsent || !cand.Gone || cand.Reason != adoptionRefusedProcessAbsent || !start.Gone {
		t.Fatalf("exited guest: item %+v, candidate %+v, startup %+v", item, cand, start)
	}

	// With an adoption block the live proof reports the same.
	g := newAdoptionFixture(t)
	g.must(g.journal.recordAdoption(g.lease, g.process, g.block()))
	if err := os.RemoveAll(filepath.Join(g.proc, fmt.Sprint(adoptionTestPID))); err != nil {
		t.Fatal(err)
	}
	items, err := g.classifier().inventory(t.Context(), g.journal)
	if err != nil || len(items) != 1 || items[0].ProofOK || items[0].ProofReason != adoptionRefusedProcessAbsent {
		t.Fatalf("exited guest with a block: inventory %+v, %v", items, err)
	}
	if v := CandidateAdoptionVerdicts(items, func(string) string { return g.jailRoot }, false); !v[0].Gone {
		t.Fatalf("exited guest with a block: candidate %+v", v[0])
	}
	for _, reason := range []string{adoptionRefusedNoRecord, adoptionRefusedUnproven, adoptionRefusedArgv, adoptionRefusedAPIUnreachable, ""} {
		if AdoptionGuestGone(reason) {
			t.Errorf("AdoptionGuestGone(%q) = true; only a proven-gone process may skip the drain", reason)
		}
	}
}
