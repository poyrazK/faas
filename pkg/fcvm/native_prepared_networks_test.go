//go:build linux || darwin

// adr: 149 — amendment 2026-11-07: prepared networks under native recovery.
package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeSpareJournal(t *testing.T) *nativeLaunchJournal {
	t.Helper()
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	j.owners, j.preparedSpares = true, true
	j.ownerID = func() (nativeProducerIdentity, error) {
		return nativeProducerIdentity{Protocol: nativeLaunchOwnerProtocol, PID: 4242, UID: 0, StartTime: 99}, nil
	}
	return j
}

func nativeSpareLease(slot int) Lease {
	return leaseForSlot("prepared-"+uuid.NewString(), slot)
}

func nativeTargetLease(instance string, slot int) Lease {
	l := leaseForSlot(instance, slot)
	l.Plan = api.PlanHobby
	return l
}

func readNativeSpareStates(t *testing.T, j *nativeLaunchJournal, instances ...string) map[string]nativePreparedState {
	t.Helper()
	var records []nativeLaunchRecord
	for _, id := range instances {
		r, err := j.read(id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	states, err := classifyNativePrepared(records)
	if err != nil {
		t.Fatal(err)
	}
	return states
}

// A spare is journaled before its host objects exist, a claim is journaled
// before the namespace moves, and recovery can tell every step apart.
func TestNativePreparedSpareClaimProtocol(t *testing.T) {
	ctx := t.Context()
	j := nativeSpareJournal(t)
	lease := nativeSpareLease(5)
	spare, err := j.prepareSpare(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if spare.Version != nativePreparedSpareVersion || !spare.spare() || spare.Prepared.Target != "" || spare.Authorized {
		t.Fatalf("spare record = %+v", spare)
	}
	if _, err := j.prepareSpare(ctx, lease); err == nil {
		t.Fatal("a spare was published twice")
	}
	const target = "target-a"
	if got := readNativeSpareStates(t, j, lease.Instance, target)[lease.Instance]; got != nativePreparedReady {
		t.Fatalf("unclaimed spare state = %d", got)
	}

	claimed, err := j.claimSpare(ctx, spare, target)
	if err != nil || claimed.Prepared.Target != target {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := j.claimSpare(ctx, spare, "target-b"); err == nil {
		t.Fatal("a claimed spare was claimed again")
	}
	// Crash after step 1: no target record, so the spare still owns its objects.
	if got := readNativeSpareStates(t, j, lease.Instance, target)[lease.Instance]; got != nativePreparedAbandoned {
		t.Fatalf("claimed spare without a target state = %d", got)
	}

	link := nativePreparedLink{Source: lease.Instance, Target: target, SourceGeneration: spare.Generation}
	if err := j.prepare(withNativePreparedClaim(ctx, link), nativeTargetLease(target, 5)); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(target)
	if err != nil || owner.Version != nativePreparedSpareVersion || owner.Prepared != link || owner.spare() {
		t.Fatalf("target record = %+v, %v", owner, err)
	}
	// Crash after step 2 or 3: the target owns the slot.
	if got := readNativeSpareStates(t, j, lease.Instance, target)[lease.Instance]; got != nativePreparedTransferred {
		t.Fatalf("linked spare state = %d", got)
	}

	moved := false
	if err := j.withPreparedTarget(ctx, target, uuid.NewString(), func(nativeLaunchRecord) error { moved = true; return nil }); err == nil || moved {
		t.Fatal("another generation moved the claimed spare")
	}
	if err := j.withPreparedTarget(ctx, target, owner.Generation, func(r nativeLaunchRecord) error { moved = r.Generation == owner.Generation; return nil }); err != nil || !moved {
		t.Fatalf("target move = %v, ran %v", err, moved)
	}

	if err := j.finishClaim(ctx, link); err != nil {
		t.Fatal(err)
	}
	if _, err := j.read(lease.Instance); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claimed spare still in the record root: %v", err)
	}
	archived, err := readNativeLaunchRecord(filepath.Join(j.root, "retired", spare.Generation+".json"), lease.Instance)
	if err != nil || !archived.Revoked || !archived.ExitConfirmed || !archived.ResourcesRemoved || archived.Prepared.Target != target {
		t.Fatalf("archived spare = %+v, %v", archived, err)
	}
	ordinary, err := j.read(target)
	if err != nil || ordinary.Version != 3 || ordinary.Prepared != (nativePreparedLink{}) || ordinary.Generation != owner.Generation {
		t.Fatalf("finished target = %+v, %v", ordinary, err)
	}
	if err := j.finishClaim(ctx, link); err != nil {
		t.Fatalf("finishing a finished claim: %v", err)
	}
	if err := j.withPreparedTarget(ctx, target, owner.Generation, func(nativeLaunchRecord) error { return nil }); err == nil {
		t.Fatal("a finished target moved a spare again")
	}
}

// Recovery finishes a claim whose spare is already archived but whose target
// still carries the link.
func TestNativePreparedFinishAfterArchive(t *testing.T) {
	ctx := t.Context()
	j := nativeSpareJournal(t)
	lease := nativeSpareLease(6)
	spare, err := j.prepareSpare(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.claimSpare(ctx, spare, "target-c"); err != nil {
		t.Fatal(err)
	}
	link := nativePreparedLink{Source: lease.Instance, Target: "target-c", SourceGeneration: spare.Generation}
	if err := j.prepare(withNativePreparedClaim(ctx, link), nativeTargetLease("target-c", 6)); err != nil {
		t.Fatal(err)
	}
	if err := j.archiveClaimedSpare(ctx, link); err != nil {
		t.Fatal(err)
	}
	if states := readNativeSpareStates(t, j, lease.Instance, "target-c"); len(states) != 0 {
		t.Fatalf("archived spare still classified: %v", states)
	}
	if err := j.finishClaim(ctx, link); err != nil {
		t.Fatal(err)
	}
	if r, err := j.read("target-c"); err != nil || r.Version != 3 {
		t.Fatalf("target after interrupted finish = %+v, %v", r, err)
	}
}

func TestNativePreparedClaimRefusals(t *testing.T) {
	ctx := t.Context()
	t.Run("spares need the setting and owner records", func(t *testing.T) {
		j := nativeSpareJournal(t)
		j.preparedSpares = false
		if _, err := j.prepareSpare(ctx, nativeSpareLease(1)); err == nil {
			t.Fatal("spare published while native_prepared_networks is off")
		}
		j = nativeSpareJournal(t)
		j.owners = false
		if _, err := j.prepareSpare(ctx, nativeSpareLease(1)); err == nil {
			t.Fatal("spare published without an owner record")
		}
	})
	t.Run("only prepared identities are spares", func(t *testing.T) {
		j := nativeSpareJournal(t)
		if _, err := j.prepareSpare(ctx, leaseForSlot("ordinary", 1)); err == nil {
			t.Fatal("an ordinary instance was published as a spare")
		}
	})
	t.Run("a spare cannot claim a spare", func(t *testing.T) {
		j := nativeSpareJournal(t)
		spare, err := j.prepareSpare(ctx, nativeSpareLease(2))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := j.claimSpare(ctx, spare, "prepared-"+uuid.NewString()); err == nil {
			t.Fatal("a spare claimed another spare")
		}
	})
	t.Run("a target links only its own claim on its slot", func(t *testing.T) {
		j := nativeSpareJournal(t)
		lease := nativeSpareLease(3)
		spare, err := j.prepareSpare(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := j.claimSpare(ctx, spare, "target-d"); err != nil {
			t.Fatal(err)
		}
		other := nativePreparedLink{Source: lease.Instance, Target: "target-e", SourceGeneration: spare.Generation}
		if err := j.prepare(withNativePreparedClaim(ctx, other), nativeTargetLease("target-e", 3)); err == nil {
			t.Fatal("a target linked a spare claimed by another instance")
		}
		link := nativePreparedLink{Source: lease.Instance, Target: "target-d", SourceGeneration: spare.Generation}
		if err := j.prepare(withNativePreparedClaim(ctx, link), nativeTargetLease("target-d", 4)); err == nil {
			t.Fatal("a target linked a spare on another slot")
		}
		stale := link
		stale.SourceGeneration = uuid.NewString()
		if err := j.prepare(withNativePreparedClaim(ctx, stale), nativeTargetLease("target-d", 3)); err == nil {
			t.Fatal("a target linked another spare generation")
		}
	})
	t.Run("an ordinary record moves no spare", func(t *testing.T) {
		j := nativeSpareJournal(t)
		lease := nativeTargetLease("ordinary-target", 7)
		if err := j.prepare(ctx, lease); err != nil {
			t.Fatal(err)
		}
		r, err := j.read(lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.withPreparedTarget(ctx, lease.Instance, r.Generation, func(nativeLaunchRecord) error { return nil }); err == nil {
			t.Fatal("an unlinked record ran a spare move")
		}
	})
}

func TestNativePreparedRecordShape(t *testing.T) {
	spare := nativeOwnerRecordFixture()
	spare.Lease = nativeSpareLease(2)
	spare.Version, spare.Prepared = nativePreparedSpareVersion, nativePreparedLink{Source: spare.Lease.Instance}
	b, err := json.Marshal(spare)
	if err != nil {
		t.Fatal(err)
	}
	var copy nativeLaunchRecord
	if err := json.Unmarshal(b, &copy); err != nil || copy != spare {
		t.Fatal("spare record round trip changed value identity", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]json.RawMessage){
		"missing link":      func(m map[string]json.RawMessage) { delete(m, "prepared") },
		"link on version 3": func(m map[string]json.RawMessage) { m["version"] = json.RawMessage(`3`) },
		"missing owner":     func(m map[string]json.RawMessage) { delete(m, "owner") },
		"link field missing": func(m map[string]json.RawMessage) {
			m["prepared"] = json.RawMessage(`{"source":"` + spare.Lease.Instance + `","target":""}`)
		},
		"link field extra": func(m map[string]json.RawMessage) {
			m["prepared"] = json.RawMessage(`{"source":"` + spare.Lease.Instance + `","target":"","source_generation":"","slot":2}`)
		},
		"spare launched": func(m map[string]json.RawMessage) {
			m["authorized"], m["pid"], m["start_time"] = json.RawMessage(`true`), json.RawMessage(`99`), json.RawMessage(`7`)
		},
		"spare names a generation": func(m map[string]json.RawMessage) {
			m["prepared"] = json.RawMessage(`{"source":"` + spare.Lease.Instance + `","target":"","source_generation":"` + uuid.NewString() + `"}`)
		},
		"source is not a spare name": func(m map[string]json.RawMessage) {
			m["prepared"] = json.RawMessage(`{"source":"ordinary","target":"","source_generation":""}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := make(map[string]json.RawMessage, len(fields))
			for k, v := range fields {
				bad[k] = v
			}
			mutate(bad)
			blob, _ := json.Marshal(bad)
			var decoded nativeLaunchRecord
			if err := json.Unmarshal(blob, &decoded); err == nil {
				if decoded.validate(decoded.Lease.Instance) == nil {
					t.Fatal("damaged spare record accepted")
				}
			}
		})
	}

	target := nativeOwnerRecordFixture()
	target.Lease = nativeTargetLease("target-shape", 2)
	target.Version, target.Prepared = nativePreparedSpareVersion, nativePreparedLink{Source: spare.Lease.Instance, Target: "target-shape", SourceGeneration: spare.Generation}
	if err := target.validate(target.Lease.Instance); err != nil {
		t.Fatalf("linked target rejected: %v", err)
	}
	for name, mutate := range map[string]func(*nativeLaunchRecord){
		"no source generation": func(r *nativeLaunchRecord) { r.Prepared.SourceGeneration = "" },
		"links another target": func(r *nativeLaunchRecord) { r.Prepared.Target = "someone-else" },
		"networkless target":   func(r *nativeLaunchRecord) { r.Lease.Networkless = true },
	} {
		t.Run(name, func(t *testing.T) {
			bad := target
			mutate(&bad)
			if bad.validate(bad.Lease.Instance) == nil {
				t.Fatal("damaged target link accepted")
			}
		})
	}
}

func TestClassifyNativePreparedRefusesForeignLinks(t *testing.T) {
	spare := nativeOwnerRecordFixture()
	spare.Lease = nativeSpareLease(2)
	spare.Version, spare.Prepared = nativePreparedSpareVersion, nativePreparedLink{Source: spare.Lease.Instance, Target: "target-x"}
	target := nativeOwnerRecordFixture()
	target.Lease = nativeTargetLease("target-y", 2)
	target.Version, target.Prepared = nativePreparedSpareVersion, nativePreparedLink{Source: spare.Lease.Instance, Target: "target-y", SourceGeneration: spare.Generation}
	if _, err := classifyNativePrepared([]nativeLaunchRecord{spare, target}); err == nil {
		t.Fatal("a target linked a spare that names another target")
	}
}

// Recovery reserves a claimed spare's slot once, through the instance that
// claimed it, while the inventory still accepts the spare's namespace.
func TestNativeRecoveryReservesTransferredSpareThroughTarget(t *testing.T) {
	ctx := t.Context()
	m, v, _ := nativeManagerFixture(t)
	j := v.nativeRecovery.journal
	j.owners, j.preparedSpares = true, true
	j.ownerID = func() (nativeProducerIdentity, error) {
		return nativeProducerIdentity{Protocol: nativeLaunchOwnerProtocol, PID: 4242, UID: 0, StartTime: 99}, nil
	}
	var inventoried []Lease
	v.nativeRecovery.inventory = func(l []Lease) error { inventoried = append([]Lease(nil), l...); return nil }

	ready := nativeSpareLease(11)
	if _, err := j.prepareSpare(ctx, ready); err != nil {
		t.Fatal(err)
	}
	moving := nativeSpareLease(12)
	spare, err := j.prepareSpare(ctx, moving)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.claimSpare(ctx, spare, "target-r"); err != nil {
		t.Fatal(err)
	}
	link := nativePreparedLink{Source: moving.Instance, Target: "target-r", SourceGeneration: spare.Generation}
	if err := j.prepare(withNativePreparedClaim(ctx, link), nativeTargetLease("target-r", 12)); err != nil {
		t.Fatal(err)
	}

	if err := m.RecoverNativeProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	recovered := make(map[string]bool, len(m.nativeRecovered))
	for id := range m.nativeRecovered {
		recovered[id] = true
	}
	m.mu.Unlock()
	if !recovered[ready.Instance] || !recovered["target-r"] || recovered[moving.Instance] {
		t.Fatalf("recovered holdings = %v; want the ready spare and the target, not the claimed spare", recovered)
	}
	names := map[string]bool{}
	for _, l := range inventoried {
		names[l.Netns] = true
	}
	if !names[moving.Netns] || !names["fc-target-r"] || !names[ready.Netns] {
		t.Fatalf("inventory leases = %v; want both names of the claim and the ready spare", names)
	}
}
