package runtimeadmission

// adr: 595 Native acknowledgments here are simulated; proof validation is real.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/proto"
)

func snapshotResumeEvidenceFixture(t *testing.T) (Promotion, SnapshotResumeEvidence, time.Time) {
	t.Helper()
	p := snapshotResumeRequestFixture(t)
	parentHash, err := HashSnapshotResumeParent(p.Parent)
	if err != nil {
		t.Fatal(err)
	}
	clock := p.Binding.IssuedAtUnixNano + int64(time.Millisecond)
	e := SnapshotResumeEvidence{Version: SnapshotResumeEvidenceVersion, Binding: p.Binding, ParentBinding: p.Parent.Binding, ParentCompletedAtUnixNano: p.Parent.CompletedAtUnixNano, ParentReceiptHash: parentHash,
		ResumeCommandHash: SnapshotResumeCommandHash(), ResumeHookPayloadHash: strings.Repeat("b", 64),
		CommandCompletedAtUnixNano: clock, HostTimeUnixNano: clock + 1, HookCompletedAtUnixNano: clock + 2, CompletedAtUnixNano: clock + 3}
	return p, e, time.Unix(0, e.CompletedAtUnixNano)
}

func TestSnapshotResumeEvidenceBindsExactHistoryGrantAndConsumption(t *testing.T) {
	p, e, now := snapshotResumeEvidenceFixture(t)
	if err := e.Check(p, p.Parent.ArtifactConsumption, p.Parent.SnapshotConsumption, now); err != nil {
		t.Fatal("complete evidence refused", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*SnapshotResumeEvidence)
	}{
		{"absent proof", func(e *SnapshotResumeEvidence) { *e = SnapshotResumeEvidence{} }},
		{"unknown version", func(e *SnapshotResumeEvidence) { e.Version++ }},
		{"another parent", func(e *SnapshotResumeEvidence) { e.ParentReceiptHash = strings.Repeat("0", 64) }},
		{"another nonce", func(e *SnapshotResumeEvidence) { e.Binding.Token = uuid.NewString() }},
		{"another process incarnation", func(e *SnapshotResumeEvidence) { e.Binding.Incarnation = uuid.NewString() }},
		{"another policy", func(e *SnapshotResumeEvidence) { e.Binding.DesiredRevision++ }},
		{"another grant expiry", func(e *SnapshotResumeEvidence) { e.Binding.ExpiresAtUnixNano++ }},
		{"another payload", func(e *SnapshotResumeEvidence) { e.Binding.PayloadHash = strings.Repeat("0", 64) }},
		{"different resume command", func(e *SnapshotResumeEvidence) { e.ResumeCommandHash = strings.Repeat("0", 64) }},
		{"missing hook", func(e *SnapshotResumeEvidence) { e.ResumeHookPayloadHash = "" }},
		{"noncanonical hook", func(e *SnapshotResumeEvidence) { e.ResumeHookPayloadHash = strings.Repeat("B", 64) }},
		{"no command clock", func(e *SnapshotResumeEvidence) { e.CommandCompletedAtUnixNano = 0 }},
		{"command before load", func(e *SnapshotResumeEvidence) { e.CommandCompletedAtUnixNano = p.Parent.CompletedAtUnixNano - 1 }},
		{"command before grant", func(e *SnapshotResumeEvidence) {
			e.CommandCompletedAtUnixNano = p.Binding.IssuedAtUnixNano - int64(api.ApplicationStandardRuntimeAdmissionClockSkew) - 1
		}},
		{"host clock before command", func(e *SnapshotResumeEvidence) { e.HostTimeUnixNano = e.CommandCompletedAtUnixNano - 1 }},
		{"hook before host clock", func(e *SnapshotResumeEvidence) { e.HookCompletedAtUnixNano = e.HostTimeUnixNano - 1 }},
		{"completion before hook", func(e *SnapshotResumeEvidence) { e.CompletedAtUnixNano = e.HookCompletedAtUnixNano - 1 }},
		{"completion at expiry", func(e *SnapshotResumeEvidence) { e.CompletedAtUnixNano = p.Binding.ExpiresAtUnixNano }},
		{"future observation", func(e *SnapshotResumeEvidence) {
			e.CompletedAtUnixNano = now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano() + 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := e
			tc.edit(&changed)
			if changed.Check(p, p.Parent.ArtifactConsumption, p.Parent.SnapshotConsumption, now) == nil {
				t.Fatal("invalid resume evidence accepted")
			}
		})
	}
	drives := p.Parent.ArtifactConsumption.Clone()
	drives.ProcessStart += "1"
	if e.Check(p, drives, p.Parent.SnapshotConsumption, now) == nil {
		t.Fatal("a different process replaced the paused load")
	}
	drives = p.Parent.ArtifactConsumption.Clone()
	drives.ConfigHash = SnapshotLoadCommandHash(false)
	if e.Check(p, drives, p.Parent.SnapshotConsumption, now) == nil {
		t.Fatal("a serving load command that was never delivered replaced history")
	}
	snapshot := p.Parent.SnapshotConsumption
	snapshot.MappedMemoryBytes--
	if e.Check(p, p.Parent.ArtifactConsumption, snapshot, now) == nil {
		t.Fatal("partial private mapping replaced the paused load")
	}
	changed := p.Clone()
	changed.Parent.NativeInputHash = strings.Repeat("0", 64)
	bindSnapshotResumePayload(t, &changed)
	e.Binding = changed.Binding
	if e.Check(changed, changed.Parent.ArtifactConsumption, changed.Parent.SnapshotConsumption, now) == nil {
		t.Fatal("fresh payload relabeled the original parent witness")
	}
	if !errors.Is(e.Check(p, p.Parent.ArtifactConsumption, p.Parent.SnapshotConsumption, time.Unix(0, p.Binding.ExpiresAtUnixNano)), ErrExpired) {
		t.Fatal("expired promotion grant accepted")
	}
}

func TestSnapshotResumeEvidenceWireRoundTripAndRefusesUnknownFields(t *testing.T) {
	p, e, now := snapshotResumeEvidenceFixture(t)
	wire := e.ToProto()
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	copy := e.ToProto()
	proto.Reset(copy)
	if err := proto.Unmarshal(raw, copy); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotResumeEvidenceFromProto(copy)
	if err != nil || got != e || got.Check(p, p.Parent.ArtifactConsumption, p.Parent.SnapshotConsumption, now) != nil {
		t.Fatal("wire lost complete resume lineage", err)
	}
	copy.Binding.Token = uuid.NewString()
	if got != e {
		t.Fatal("wire caller changed retained evidence")
	}
	for _, field := range []string{"proof", "binding", "version", "missing binding"} {
		t.Run(field, func(t *testing.T) {
			wire := e.ToProto()
			switch field {
			case "proof":
				wire.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "binding":
				wire.Binding.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "version":
				wire.Version++
			case "missing binding":
				wire.Binding = nil
			}
			if value, err := SnapshotResumeEvidenceFromProto(wire); err == nil || !value.IsZero() {
				t.Fatal("incomplete or unknown proof crossed the wire boundary")
			}
		})
	}
	if (SnapshotResumeEvidence{}).ToProto() != nil {
		t.Fatal("absent evidence gained a wire representation")
	}
	if value, err := SnapshotResumeEvidenceFromProto(nil); err == nil || !value.IsZero() {
		t.Fatal("absent evidence decoded as authority")
	}
	jsonRaw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var jsonCopy SnapshotResumeEvidence
	if err := json.Unmarshal(jsonRaw, &jsonCopy); err != nil || jsonCopy != e {
		t.Fatal("JSON changed proof identity", err)
	}
	// A paused load alone still cannot authorize serving publication.
	if p.Validate(now) != nil || p.CheckReceipt(p.Parent, now) == nil {
		t.Fatal("paused parent crossed the serving boundary")
	}
}
