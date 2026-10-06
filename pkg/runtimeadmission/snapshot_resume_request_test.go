package runtimeadmission

// adr: 595 Historical native identities here are explicitly simulated.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func snapshotResumeRequestFixture(t *testing.T) Promotion {
	t.Helper()
	r, _ := snapshotConsumedReceiptFixture(t)
	r.Paused = true
	r.ArtifactConsumption.ConfigHash = SnapshotLoadCommandHash(true)
	r.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
	r.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
	r.CompletedAtUnixNano -= int64(2 * time.Hour)
	b := r.Binding
	b.Token = uuid.NewString()
	b.IssuedAtUnixNano, b.ExpiresAtUnixNano = time.Now().UnixNano(), time.Now().Add(time.Minute).UnixNano()
	p := Promotion{Binding: b, Parent: r}
	bindSnapshotResumePayload(t, &p)
	return p
}

func bindSnapshotResumePayload(t *testing.T, p *Promotion) {
	t.Helper()
	var err error
	p.Binding.PayloadHash, err = HashPromotionPayload(p.ToProto())
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotResumeRequestUsesFreshGrantAndPreservesPublicRefusal(t *testing.T) {
	p := snapshotResumeRequestFixture(t)
	if !errors.Is(p.Parent.Binding.Validate(time.Now()), ErrExpired) {
		t.Fatal("historical load authority was renewed")
	}
	if err := p.CheckSnapshotResumeRequest(time.Now()); err != nil {
		t.Fatal("fresh native request refused", err)
	}
	if !errors.Is(p.Parent.Check(p.Parent.Binding, time.Unix(0, p.Parent.CompletedAtUnixNano)), ErrUnavailable) || p.Validate(time.Now()) == nil || p.CheckReceipt(p.Parent, time.Now()) == nil {
		t.Fatal("private request enabled public paused promotion")
	}
	decoded, err := PromotionFromProto(p.ToProto())
	if err != nil || !decoded.Equal(p) || decoded.CheckSnapshotResumeRequest(time.Now()) != nil {
		t.Fatal("private request lost complete lineage on wire", err)
	}
	copy := p.Clone()
	copy.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
	if p.Equal(copy) || p.Parent.ArtifactConsumption.Drives[0].DriveID == "caller-edit" {
		t.Fatal("request does not own its parent bytes")
	}
}

func TestSnapshotResumeRequestRefusesIncompleteOrReboundHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Promotion)
	}{
		{"replayed token", func(p *Promotion) { p.Binding.Token = p.Parent.Binding.Token }},
		{"different instance", func(p *Promotion) { p.Binding.InstanceID = uuid.NewString() }},
		{"different policy", func(p *Promotion) { p.Binding.EffectiveHash = strings.Repeat("0", 64) }},
		{"different incarnation", func(p *Promotion) { p.Binding.Incarnation = uuid.NewString() }},
		{"different evidence", func(p *Promotion) { p.Binding.SnapshotEvidenceHash = strings.Repeat("b", 64) }},
		{"expired fresh grant", func(p *Promotion) {
			p.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
			p.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
		}},
		{"future fresh grant", func(p *Promotion) {
			p.Binding.IssuedAtUnixNano += int64(2 * time.Hour)
			p.Binding.ExpiresAtUnixNano += int64(2 * time.Hour)
		}},
		{"already serving", func(p *Promotion) { p.Parent.Paused = false }},
		{"cold parent", func(p *Promotion) { p.Parent.Method = vmmdpb.WakeMethod_WAKE_COLD_BOOT }},
		{"missing mapping", func(p *Promotion) { p.Parent.SnapshotConsumption = SnapshotConsumption{} }},
		{"partial mapping", func(p *Promotion) { p.Parent.SnapshotConsumption.MappedMemoryBytes-- }},
		{"missing drives", func(p *Promotion) { p.Parent.ArtifactConsumption.Drives = nil }},
		{"invented serving load", func(p *Promotion) { p.Parent.ArtifactConsumption.ConfigHash = SnapshotLoadCommandHash(false) }},
		{"no process", func(p *Promotion) { p.Parent.ArtifactConsumption.ProcessPID = 0 }},
		{"no start time", func(p *Promotion) { p.Parent.ArtifactConsumption.ProcessStart = "" }},
		{"no completion", func(p *Promotion) { p.Parent.CompletedAtUnixNano = 0 }},
		{"expired load at completion", func(p *Promotion) { p.Parent.CompletedAtUnixNano = p.Parent.Binding.ExpiresAtUnixNano }},
		{"no lease UID", func(p *Promotion) { p.Parent.LeaseUID = 0 }},
		{"invalid host IP", func(p *Promotion) { p.Parent.HostIP = "0.0.0.0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := snapshotResumeRequestFixture(t)
			tc.edit(&p)
			bindSnapshotResumePayload(t, &p)
			if p.CheckSnapshotResumeRequest(time.Now()) == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	p := snapshotResumeRequestFixture(t)
	p.Parent.NativeInputHash = strings.Repeat("c", 64)
	if p.CheckSnapshotResumeRequest(time.Now()) == nil {
		t.Fatal("parent edit escaped the exact fresh payload hash")
	}
}
