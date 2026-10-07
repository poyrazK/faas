package runtimeadmission

// adr: 595. Catalog bytes are bound to fresh authority, not renewed by history.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/proto"
)

const SnapshotRestoreVersion = 1

type SnapshotRestoreEvidence struct {
	Version      uint32          `json:"version"`
	CaptureToken string          `json:"capture_token"`
	FCVersion    string          `json:"fc_version"`
	Capture      SnapshotCapture `json:"capture"`
}

func (b Binding) validSnapshotBinding() bool {
	if b.SnapshotCaptureToken == "" && b.SnapshotEvidenceHash == "" {
		return true
	}
	return b.ProtocolVersion == ArtifactProtocolVersion && canonicalUUID(b.SnapshotCaptureToken) && ValidHash(b.SnapshotEvidenceHash)
}

func (e SnapshotRestoreEvidence) Clone() SnapshotRestoreEvidence {
	e.Capture = e.Capture.Clone()
	return e
}

func (e SnapshotRestoreEvidence) Validate(now time.Time) error {
	if e.Version != SnapshotRestoreVersion || !canonicalUUID(e.CaptureToken) || e.Capture.Check(now) != nil || !strings.HasSuffix(e.Capture.Memory.StorageKey, "/captures/"+e.CaptureToken+"/v2/mem") {
		return ErrInvalid
	}
	if e.FCVersion == "" || len(e.FCVersion) > api.ApplicationStandardSnapshotMaxFCVersionBytes || strings.TrimSpace(e.FCVersion) != e.FCVersion || strings.ContainsAny(e.FCVersion, "\x00\r\n\t /\\") {
		return ErrInvalid
	}
	return nil
}

func (e SnapshotRestoreEvidence) Hash() (string, error) {
	if err := e.Validate(time.Now()); err != nil {
		return "", err
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(e.ToProto())
	if err != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(append([]byte("gregale.snapshot-restore-evidence.v1\x00"), raw...))
	return hex.EncodeToString(h[:]), nil
}

func (e SnapshotRestoreEvidence) Check(binding Binding, sources []ArtifactSource, memory, vmstate, fcVersion string, memoryBytes int64, now time.Time) error {
	if err := e.Validate(now); err != nil {
		return err
	}
	hash, err := e.Hash()
	if err != nil || e.CaptureToken != binding.SnapshotCaptureToken || hash != binding.SnapshotEvidenceHash || e.FCVersion != fcVersion {
		return ErrInvalid
	}
	return e.Capture.CheckRestoreInputs(binding, sources, memory, vmstate, memoryBytes, now)
}

func (e SnapshotRestoreEvidence) ToProto() *vmmdpb.RuntimeSnapshotRestoreEvidence {
	return &vmmdpb.RuntimeSnapshotRestoreEvidence{Version: e.Version, CaptureToken: e.CaptureToken, FcVersion: e.FCVersion, Capture: e.Capture.ToProto()}
}

func SnapshotRestoreEvidenceFromProto(p *vmmdpb.RuntimeSnapshotRestoreEvidence) (SnapshotRestoreEvidence, error) {
	if p == nil || RejectUnknown(p) != nil {
		return SnapshotRestoreEvidence{}, ErrInvalid
	}
	capture, err := SnapshotCaptureFromProto(p.Capture)
	if err != nil {
		return SnapshotRestoreEvidence{}, err
	}
	e := SnapshotRestoreEvidence{Version: p.Version, CaptureToken: p.CaptureToken, FCVersion: p.FcVersion, Capture: capture}
	return e, e.Validate(time.Now())
}
