package runtimeadmission

// adr: 435. Captured bytes are lineage evidence, never a restore grant or scan.

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

const SnapshotCaptureVersion = 1

type CapturedArtifact struct {
	StorageKey string `json:"storage_key"`
	Digest     string `json:"digest"`
	Bytes      int64  `json:"bytes"`
}

// SnapshotCapture binds all three paused artifacts to a historical measured
// boot. Its parent may have expired since boot. A fresh durable capture review
// and restore admission must independently establish current policy authority.
type SnapshotCapture struct {
	Version            uint32           `json:"version"`
	Parent             Receipt          `json:"parent"`
	Memory             CapturedArtifact `json:"memory"`
	VMState            CapturedArtifact `json:"vmstate"`
	PrivateDrive       CapturedArtifact `json:"private_drive"`
	CapturedAtUnixNano int64            `json:"captured_at_unix_nano"`
}

func (c SnapshotCapture) IsZero() bool {
	return c.Version == 0 && c.Parent.Equal(Receipt{}) && c.Memory == (CapturedArtifact{}) && c.VMState == (CapturedArtifact{}) && c.PrivateDrive == (CapturedArtifact{}) && c.CapturedAtUnixNano == 0
}

func (c SnapshotCapture) Clone() SnapshotCapture {
	c.Parent = c.Parent.Clone()
	return c
}

func (c SnapshotCapture) Equal(other SnapshotCapture) bool {
	return c.Version == other.Version && c.Parent.Equal(other.Parent) && c.Memory == other.Memory && c.VMState == other.VMState && c.PrivateDrive == other.PrivateDrive && c.CapturedAtUnixNano == other.CapturedAtUnixNano
}

func (c SnapshotCapture) Check(now time.Time) error {
	if c.Version != SnapshotCaptureVersion || c.Parent.Binding.ProtocolVersion != ArtifactProtocolVersion || !c.Parent.SnapshotConsumption.IsZero() || c.Parent.CompletedAtUnixNano <= 0 || c.Parent.Check(c.Parent.Binding, time.Unix(0, c.Parent.CompletedAtUnixNano)) != nil {
		return ErrInvalid
	}
	if c.CapturedAtUnixNano < c.Parent.CompletedAtUnixNano || time.Unix(0, c.CapturedAtUnixNano).After(now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew)) {
		return ErrInvalid
	}
	if err := CheckSnapshotCaptureKeys(c.Parent.Binding.DeploymentID, c.Memory.StorageKey, c.VMState.StorageKey, c.PrivateDrive.StorageKey); err != nil {
		return err
	}
	for _, artifact := range []CapturedArtifact{c.Memory, c.VMState, c.PrivateDrive} {
		if artifact.Bytes <= 0 || artifact.Bytes > api.ApplicationStandardSnapshotMaxArtifactBytes || ociref.ValidateDigest(artifact.Digest) != nil {
			return ErrInvalid
		}
	}
	for _, drive := range c.Parent.ArtifactConsumption.Drives {
		if drive.Source.Role() == "main" && c.PrivateDrive.Bytes != drive.InjectedBytes {
			return ErrInvalid
		}
	}
	return nil
}

// Accept the legacy compact deployment UUID spelling, but retain exact keys.
// Only a fresh UUID namespace with the coupled v2 layout can carry evidence.
func CheckSnapshotCaptureKeys(deploymentID, memory, vmstate, drive string) error {
	if len(memory) > api.ApplicationStandardBaseMaxStorageKeyBytes || !canonicalUUID(deploymentID) {
		return ErrInvalid
	}
	parts := strings.Split(memory, "/")
	if len(parts) == 7 && parts[2] == "warm" {
		parts = append(parts[:2:2], parts[3:]...)
	}
	if len(parts) != 6 || parts[0] != "snap" || parts[2] != "captures" || !canonicalUUID(parts[3]) || parts[4] != "v2" || parts[5] != "mem" {
		return ErrInvalid
	}
	dep, err := uuid.Parse(parts[1])
	if err != nil || dep.String() != deploymentID || parts[1] != deploymentID && parts[1] != strings.ReplaceAll(deploymentID, "-", "") {
		return ErrInvalid
	}
	prefix := strings.TrimSuffix(memory, "mem")
	if vmstate != prefix+"vmstate" || drive != prefix+"drive" {
		return ErrInvalid
	}
	return nil
}
