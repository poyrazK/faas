package runtimeadmission

// adr: 1664. A migration retains its logical ID, with fresh physical authority.

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

type FunctionMigrationDestinationGrant struct {
	Version uint32                  `json:"version"`
	Source  FunctionMigrationGrant  `json:"source"`
	Restore SnapshotRestoreEvidence `json:"restore"`
	Binding Binding                 `json:"binding"`
	WakeID  string                  `json:"wake_id"`
}

func (g FunctionMigrationDestinationGrant) Clone() FunctionMigrationDestinationGrant {
	g.Source, g.Restore = g.Source.Clone(), g.Restore.Clone()
	return g
}

func (g FunctionMigrationDestinationGrant) Equal(other FunctionMigrationDestinationGrant) bool {
	return g.Version == other.Version && g.Source.Equal(other.Source) && g.Binding == other.Binding && g.WakeID == other.WakeID &&
		g.Restore.Version == other.Restore.Version && g.Restore.CaptureToken == other.Restore.CaptureToken && g.Restore.FCVersion == other.Restore.FCVersion && g.Restore.Capture.Equal(other.Restore.Capture)
}

func (g FunctionMigrationDestinationGrant) Validate(now time.Time) error {
	b, s, c := g.Binding, g.Source, g.Restore.Capture
	if g.Version != 1 || b.Validate(now) != nil || s.Validate(time.Unix(0, s.IssuedAtUnixNano)) != nil ||
		s.Purpose != FunctionMigrationCapture || g.Restore.Validate(now) != nil || !canonicalUUID(g.WakeID) || g.WakeID == s.SourceWakeID ||
		b.ProtocolVersion != ArtifactProtocolVersion || b.InstanceID != s.Parent.Binding.InstanceID || b.NodeID == s.Parent.Binding.NodeID ||
		b.Token == s.Token || b.Token == s.Parent.Binding.Token || b.Token == s.LeaseToken ||
		b.ExpiresAtUnixNano > s.LeaseExpiresAtUnixNano || b.IssuedAtUnixNano < c.CapturedAtUnixNano ||
		c.CapturedAtUnixNano < s.IssuedAtUnixNano || c.CapturedAtUnixNano >= s.ExpiresAtUnixNano || !c.Parent.Equal(s.Parent) ||
		g.Restore.CaptureToken != s.CaptureToken || g.Restore.FCVersion != s.FCVersion ||
		c.Memory.StorageKey != s.MemoryKey || c.VMState.StorageKey != s.VMStateKey || c.PrivateDrive.StorageKey != s.PrivateDriveKey {
		return ErrInvalid
	}
	hash, err := g.Restore.Hash()
	if err != nil || hash != b.SnapshotEvidenceHash || b.SnapshotCaptureToken != s.CaptureToken {
		return ErrInvalid
	}
	return nil
}

// Ordinary snapshot clones still reject the same instance ID. Only this exact
// lease/capture/destination capability permits a logical migration identity.
func (g FunctionMigrationDestinationGrant) CheckRestore(b Binding, sources []ArtifactSource, e SnapshotRestoreEvidence, memory, vmstate, fc string, bytes int64, now time.Time) error {
	if g.Validate(now) != nil || g.Binding != b || !g.Restore.Capture.Equal(e.Capture) || g.Restore.Version != e.Version ||
		g.Restore.CaptureToken != e.CaptureToken || g.Restore.FCVersion != e.FCVersion || fc != g.Restore.FCVersion ||
		memory != g.Source.MemoryKey || vmstate != g.Source.VMStateKey {
		return ErrStale
	}
	return e.Capture.checkRestoreInputs(b, sources, memory, vmstate, bytes, now, true)
}

func (g FunctionMigrationDestinationGrant) CheckConsumption(b Binding, drives ArtifactConsumption, paused bool, c SnapshotConsumption, now time.Time) error {
	if paused || c.Check(b, drives, false) != nil || c.Memory != g.Restore.Capture.Memory || c.VMState != g.Restore.Capture.VMState || c.PrivateDrive != g.Restore.Capture.PrivateDrive {
		return ErrInvalid
	}
	sources := make([]ArtifactSource, 0, len(drives.Drives))
	for _, d := range drives.Drives {
		sources = append(sources, d.Source)
	}
	return g.CheckRestore(b, sources, g.Restore, c.Memory.StorageKey, c.VMState.StorageKey, g.Restore.FCVersion, c.Memory.Bytes, now)
}

func (g FunctionMigrationDestinationGrant) CheckReceipt(r Receipt, now time.Time) error {
	if r.Check(g.Binding, now) != nil || r.Paused || r.Method != vmmdpb.WakeMethod_WAKE_RESTORE || !r.SnapshotResumeEvidence.IsZero() ||
		r.NativeInputHash != g.Binding.PayloadHash {
		return ErrInvalid
	}
	return g.CheckConsumption(r.Binding, r.ArtifactConsumption, r.Paused, r.SnapshotConsumption, now)
}
