package runtimeadmission

// adr: 595
// Restore consumption is about the process and load command in ArtifactConsumption.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

const (
	SnapshotMemoryName  = "snap-in-mem"
	SnapshotVMStateName = "snap-in-vmstate"
)

// SnapshotConsumption records verified captured backing and complete private
// memory mapping. It shares ArtifactConsumption's process identity and config
// hash; only the native observer may establish these facts. It does not attest
// resident pages, guest CPU state or guest readiness.
type SnapshotConsumption struct {
	Version           uint32           `json:"version"`
	CaptureToken      string           `json:"capture_token"`
	EvidenceHash      string           `json:"evidence_hash"`
	Memory            CapturedArtifact `json:"memory"`
	VMState           CapturedArtifact `json:"vmstate"`
	PrivateDrive      CapturedArtifact `json:"private_drive"`
	MappedMemoryBytes int64            `json:"mapped_memory_bytes"`
}

func (c SnapshotConsumption) IsZero() bool { return c == (SnapshotConsumption{}) }

func SnapshotLoadCommandHash(paused bool) string {
	raw, _ := json.Marshal(map[string]any{"snapshot_path": SnapshotVMStateName,
		"mem_backend": map[string]any{"backend_type": "File", "backend_path": SnapshotMemoryName}, "resume_vm": !paused})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func (c SnapshotConsumption) Check(b Binding, drives ArtifactConsumption, paused bool) error {
	if c.Version != SnapshotRestoreVersion || b.ProtocolVersion != ArtifactProtocolVersion ||
		!canonicalUUID(c.CaptureToken) || c.CaptureToken != b.SnapshotCaptureToken || !ValidHash(c.EvidenceHash) || c.EvidenceHash != b.SnapshotEvidenceHash ||
		drives.Check(b.ArtifactSourcesHash) != nil || drives.ConfigHash != SnapshotLoadCommandHash(paused) ||
		c.MappedMemoryBytes != c.Memory.Bytes || c.Memory.Bytes <= 0 || c.Memory.Bytes%(1<<20) != 0 ||
		CheckSnapshotCaptureKeys(b.DeploymentID, c.Memory.StorageKey, c.VMState.StorageKey, c.PrivateDrive.StorageKey) != nil ||
		!strings.HasSuffix(c.Memory.StorageKey, "/captures/"+c.CaptureToken+"/v2/mem") {
		return ErrInvalid
	}
	for _, a := range []CapturedArtifact{c.Memory, c.VMState, c.PrivateDrive} {
		if a.Bytes <= 0 || a.Bytes > api.ApplicationStandardSnapshotMaxArtifactBytes || ociref.ValidateDigest(a.Digest) != nil {
			return ErrInvalid
		}
	}
	for _, d := range drives.Drives {
		if d.Source.Role() == "main" && c.PrivateDrive.Bytes != d.InjectedBytes {
			return ErrInvalid
		}
	}
	return nil
}

// CheckEvidence compares consumption with the immutable selected catalog,
// independently of the native producer's assertion about consumed backing.
func (c SnapshotConsumption) CheckEvidence(b Binding, drives ArtifactConsumption, paused bool, e SnapshotRestoreEvidence, now time.Time) error {
	if err := c.Check(b, drives, paused); err != nil {
		return err
	}
	if c.Memory != e.Capture.Memory || c.VMState != e.Capture.VMState || c.PrivateDrive != e.Capture.PrivateDrive {
		return ErrInvalid
	}
	sources := make([]ArtifactSource, 0, len(drives.Drives))
	for _, d := range drives.Drives {
		sources = append(sources, d.Source)
	}
	return e.Check(b, sources, c.Memory.StorageKey, c.VMState.StorageKey, e.FCVersion, c.Memory.Bytes, now)
}
