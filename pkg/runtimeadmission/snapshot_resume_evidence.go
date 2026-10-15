package runtimeadmission

// adr: 595 Measured resume retains paused-load history and fresh grant identity.

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/proto"
)

const SnapshotResumeEvidenceVersion = 1

// SnapshotResumeEvidence records acknowledgments coupled to an owned paused
// load. ParentBinding and ParentCompletedAtUnixNano allow a serving receipt to
// reconstruct its complete original parent without a recursive receipt tree.
// Only the native owner can establish the command, hook and process facts;
// Check independently binds those facts to the complete historical receipt and
// fresh grant. The original paused load hash is never replaced.
type SnapshotResumeEvidence struct {
	Version                    uint32  `json:"version"`
	Binding                    Binding `json:"binding"`
	ParentReceiptHash          string  `json:"parent_receipt_hash"`
	ResumeCommandHash          string  `json:"resume_command_hash"`
	ResumeHookPayloadHash      string  `json:"resume_hook_payload_hash"`
	CommandCompletedAtUnixNano int64   `json:"command_completed_at_unix_nano"`
	HostTimeUnixNano           int64   `json:"host_time_unix_nano"`
	HookCompletedAtUnixNano    int64   `json:"hook_completed_at_unix_nano"`
	CompletedAtUnixNano        int64   `json:"completed_at_unix_nano"`
	ParentBinding              Binding `json:"parent_binding"`
	ParentCompletedAtUnixNano  int64   `json:"parent_completed_at_unix_nano"`
}

func (e SnapshotResumeEvidence) IsZero() bool { return e == (SnapshotResumeEvidence{}) }

// SnapshotResumeCommandHash identifies the exact strict PATCH /vm body. A
// different well-formed hash does not acknowledge the expected resume command.
func SnapshotResumeCommandHash() string {
	h := sha256.Sum256([]byte("gregale.runtime-resume.command.v1\x00" + `{"state":"Resumed"}`))
	return hex.EncodeToString(h[:])
}

// HashSnapshotResumeParent retains every receipt field, including the accepted
// paused load command, process start, exact drives and complete private mapping.
// The fresh Promotion payload separately binds this same historical parent.
func HashSnapshotResumeParent(r Receipt) (string, error) {
	if r.Binding.ProtocolVersion != ArtifactProtocolVersion || !r.Paused || !r.SnapshotResumeEvidence.IsZero() || r.Method != vmmdpb.WakeMethod_WAKE_RESTORE || r.CompletedAtUnixNano <= 0 ||
		r.checkRuntimeIdentity(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil ||
		r.SnapshotConsumption.Check(r.Binding, r.ArtifactConsumption, true) != nil {
		return "", ErrInvalid
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(r.ToProto())
	if err != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(append([]byte("gregale.runtime-snapshot-resume.parent.v1\x00"), raw...))
	return hex.EncodeToString(h[:]), nil
}

func (e SnapshotResumeEvidence) Check(p Promotion, drives ArtifactConsumption, snapshot SnapshotConsumption, now time.Time) error {
	if err := p.CheckSnapshotResumeRequest(now); err != nil {
		return err
	}
	parentHash, err := HashSnapshotResumeParent(p.Parent)
	if err != nil || e.Version != SnapshotResumeEvidenceVersion || e.Binding != p.Binding || e.ParentBinding != p.Parent.Binding || e.ParentCompletedAtUnixNano != p.Parent.CompletedAtUnixNano || e.ParentReceiptHash != parentHash ||
		e.ResumeCommandHash != SnapshotResumeCommandHash() || !ValidHash(e.ResumeHookPayloadHash) ||
		!drives.Equal(p.Parent.ArtifactConsumption) || snapshot != p.Parent.SnapshotConsumption {
		return ErrInvalid
	}
	issued := time.Unix(0, p.Binding.IssuedAtUnixNano).Add(-api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano()
	if e.CommandCompletedAtUnixNano <= 0 || e.CommandCompletedAtUnixNano < p.Parent.CompletedAtUnixNano || e.CommandCompletedAtUnixNano < issued ||
		e.HostTimeUnixNano < e.CommandCompletedAtUnixNano || e.HookCompletedAtUnixNano < e.HostTimeUnixNano || e.CompletedAtUnixNano < e.HookCompletedAtUnixNano ||
		e.CompletedAtUnixNano >= p.Binding.ExpiresAtUnixNano || e.CompletedAtUnixNano > now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano() {
		return ErrInvalid
	}
	return nil
}

// CheckReceipt validates the original paused load and fresh promotion payload
// independently of a database lookup. Stores must additionally match that
// reconstructed parent against their immutable, issued boot history.
func (e SnapshotResumeEvidence) CheckReceipt(r Receipt, now time.Time) error {
	if r.Paused || r.Binding != e.Binding || r.CompletedAtUnixNano != e.CompletedAtUnixNano || r.SnapshotResumeEvidence != e {
		return ErrInvalid
	}
	parent := r.Clone()
	parent.Binding, parent.Paused, parent.CompletedAtUnixNano = e.ParentBinding, true, e.ParentCompletedAtUnixNano
	parent.SnapshotResumeEvidence = SnapshotResumeEvidence{}
	return e.Check(Promotion{Binding: r.Binding, Parent: parent}, r.ArtifactConsumption, r.SnapshotConsumption, now)
}
