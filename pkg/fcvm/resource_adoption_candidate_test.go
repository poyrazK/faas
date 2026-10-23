package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"bytes"
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
