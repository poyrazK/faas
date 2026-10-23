package fcvm

// The pre-restart capability check (vmmd restart adoption ADR §4, slice 4).
// A release decides whether it can restart vmmd without draining the node
// by asking the candidate binary, before the restart, whether it would adopt
// every live guest. The running vmmd keeps its journal locked, so it hands
// the candidate its records and the live part of the proof (steps 2–6); the
// candidate judges each record with its own decoder and rules (step 1 and
// the adoption scope). A record the candidate cannot decode, from a newer
// release for instance, is not adoptable, so the release drains.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// AdoptionInventoryRecord is one journal record as the running vmmd holds it,
// with the result of the live proof it ran for it.
type AdoptionInventoryRecord struct {
	Record      []byte // the journal record's JSON
	ProofOK     bool
	ProofReason string
	ProofDetail string
}

// Refusal reasons only the candidate check gives.
const (
	adoptionRefusedUndecodable = "undecodable_record"
	adoptionRefusedUnproven    = "not_proven_live"
)

// AdoptionInventory returns every journal record except prepared spares,
// which a planned stop closes, with the live proof for each record that has
// a launched guest and an adoption block. It reads, never changes, anything.
func (m *Manager) AdoptionInventory(ctx context.Context) ([]AdoptionInventoryRecord, error) {
	if m.resourceJournal == nil {
		return nil, errors.New("adoption inventory needs the resource journal")
	}
	boot, err := resourceKernelBootID("/proc")
	if err != nil {
		return nil, fmt.Errorf("adoption inventory: kernel boot id: %w", err)
	}
	c := adoptionClassifier{
		procRoot: "/proc", cgroupRoot: cgroupRoot, netRoot: "/sys/class/net",
		bootID: boot, api: firecrackerAPIState, pidfd: adoptionPidfdCheck, vethPeer: vethPeerInProcessNetns,
	}
	return c.inventory(ctx, m.resourceJournal)
}

func (c adoptionClassifier) inventory(ctx context.Context, j *ResourceJournal) ([]AdoptionInventoryRecord, error) {
	records, err := j.snapshot()
	if err != nil {
		return nil, err
	}
	out := make([]AdoptionInventoryRecord, 0, len(records))
	for _, r := range records {
		if r.preparedSpare() {
			continue
		}
		raw, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("adoption inventory: encode %s: %w", r.Lease.Instance, err)
		}
		item := AdoptionInventoryRecord{Record: raw, ProofReason: adoptionRefusedNoRecord, ProofDetail: "no launched guest with an adoption block"}
		if r.Process != nil && r.Adoption != nil {
			_, v, ok := c.prove(ctx, r)
			item = AdoptionInventoryRecord{Record: raw, ProofOK: ok, ProofReason: v.Reason, ProofDetail: v.Detail}
		}
		out = append(out, item)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CandidateAdoptionVerdicts is the candidate binary's decision about each
// record a running vmmd reported. chrootRoot is where the candidate's
// Firecracker keeps an instance's jail (JailerChrootRoot); a changed
// Firecracker binary name refuses, as at startup. adoptAdmitted is the
// candidate's adopt_admitted_runtimes.
func CandidateAdoptionVerdicts(items []AdoptionInventoryRecord, chrootRoot func(string) string, adoptAdmitted bool) []AdoptionVerdict {
	out := make([]AdoptionVerdict, 0, len(items))
	for _, item := range items {
		r, err := decodeResourceJournalRecord(item.Record)
		if err != nil {
			out = append(out, refuse(recordInstance(item.Record), adoptionRefusedUndecodable, err.Error()))
			continue
		}
		id := r.Lease.Instance
		resolved, refused, ok := classifyRecord(r)
		switch {
		case !ok:
			out = append(out, refused)
		case !item.ProofOK:
			reason := item.ProofReason
			if reason == "" {
				reason = adoptionRefusedUnproven
			}
			out = append(out, refuse(id, reason, item.ProofDetail))
		default:
			if reason, detail := adoptionScope(chrootRoot, adoptAdmitted, r); reason != "" {
				out = append(out, refuse(id, reason, detail))
				continue
			}
			v := AdoptionVerdict{Instance: id, Adoptable: true, Resolved: resolved}
			if r.Adoption != nil {
				v.AppID = r.Adoption.Instance.AppID
			}
			out = append(out, v)
		}
	}
	return out
}

// JailerChrootRoot is the jail root a JailerVMM built by this binary gives an
// instance, for the candidate check.
func JailerChrootRoot(instance string) string {
	return filepath.Join(JailChrootBase, resolveFCChrootName(), instance, "root")
}

// decodeResourceJournalRecord applies the journal's own strictness to a
// record that came over the wire: size bound, no unknown fields, no
// trailing data, full validation.
func decodeResourceJournalRecord(raw []byte) (resourceJournalRecord, error) {
	var r resourceJournalRecord
	if len(raw) > resourceRecordLimit {
		return r, errors.New("oversized resource journal record")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, errors.New("trailing resource journal data")
	}
	return r, r.validate()
}

// recordInstance names an undecodable record's instance when it can.
func recordInstance(raw []byte) string {
	var r struct {
		Lease struct{ Instance string } `json:"lease"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Lease.Instance == "" {
		return "unknown"
	}
	return r.Lease.Instance
}
