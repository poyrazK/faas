package runtimeadmission

// adr: 1577

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func functionDestinationGrantFixture(t *testing.T) FunctionMigrationDestinationGrant {
	s := functionMigrationGrantFixture(t)
	c := snapshotCaptureFixture(t)
	c.Parent = s.Parent.Clone()
	c.Memory.StorageKey, c.VMState.StorageKey, c.PrivateDrive.StorageKey = s.MemoryKey, s.VMStateKey, s.PrivateDriveKey
	c.Memory.Bytes = 1 << 20
	c.CapturedAtUnixNano = time.Now().UnixNano()
	for _, d := range s.Parent.ArtifactConsumption.Drives {
		if d.Source.Role() == "main" {
			c.PrivateDrive.Bytes = d.InjectedBytes
		}
	}
	e := SnapshotRestoreEvidence{Version: SnapshotRestoreVersion, CaptureToken: s.CaptureToken, FCVersion: s.FCVersion, Capture: c}
	b := s.Parent.Binding
	b.Token, b.NodeID, b.Incarnation = uuid.NewString(), uuid.NewString(), uuid.NewString()
	b.CapturedInputHash = strings.Repeat("a", 64)
	b.SnapshotCaptureToken = s.CaptureToken
	b.SnapshotEvidenceHash, _ = e.Hash()
	b.IssuedAtUnixNano, b.ExpiresAtUnixNano = time.Now().UnixNano(), time.Now().Add(40*time.Second).UnixNano()
	return FunctionMigrationDestinationGrant{Version: 1, Source: s, Restore: e, Binding: b, WakeID: uuid.NewString()}
}
func TestFunctionDestinationGrantScopeAndClone(t *testing.T) {
	g := functionDestinationGrantFixture(t)
	if err := g.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	var sources []ArtifactSource
	for _, d := range g.Source.Parent.ArtifactConsumption.Drives {
		sources = append(sources, d.Source)
	}
	if err := g.CheckRestore(g.Binding, sources, g.Restore, g.Source.MemoryKey, g.Source.VMStateKey, g.Source.FCVersion, g.Restore.Capture.Memory.Bytes, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := g.Restore.Check(g.Binding, sources, g.Source.MemoryKey, g.Source.VMStateKey, g.Source.FCVersion, g.Restore.Capture.Memory.Bytes, time.Now()); err == nil {
		t.Fatal("migration weakened ordinary clone identity")
	}
	for _, test := range []struct {
		name string
		edit func(*FunctionMigrationDestinationGrant)
	}{
		{"source node", func(g *FunctionMigrationDestinationGrant) { g.Binding.NodeID = g.Source.Parent.Binding.NodeID }},
		{"source wake", func(g *FunctionMigrationDestinationGrant) { g.WakeID = g.Source.SourceWakeID }},
		{"new logical instance", func(g *FunctionMigrationDestinationGrant) { g.Binding.InstanceID = uuid.NewString() }},
		{"capture keys", func(g *FunctionMigrationDestinationGrant) { g.Restore.Capture.Memory.StorageKey = "other" }},
		{"capture digest", func(g *FunctionMigrationDestinationGrant) {
			g.Restore.Capture.Memory.Digest = "sha256:" + strings.Repeat("d", 64)
		}},
		{"capture clock", func(g *FunctionMigrationDestinationGrant) {
			g.Restore.Capture.CapturedAtUnixNano = g.Source.ExpiresAtUnixNano
		}},
		{"lease expiry", func(g *FunctionMigrationDestinationGrant) {
			g.Binding.ExpiresAtUnixNano = g.Source.LeaseExpiresAtUnixNano + 1
		}},
		{"legacy protocol", func(g *FunctionMigrationDestinationGrant) { g.Binding.ProtocolVersion = ProtocolVersion }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := g.Clone()
			test.edit(&bad)
			if bad.Validate(time.Now()) == nil {
				t.Fatal("invalid destination scope accepted")
			}
			if !g.Equal(g.Clone()) {
				t.Fatal("caller mutated owned grant")
			}
		})
	}
	if g.Validate(time.Unix(0, g.Binding.ExpiresAtUnixNano)) == nil {
		t.Fatal("expired destination grant accepted")
	}
	clone := g.Clone()
	clone.Source.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
	if g.Equal(clone) || g.Source.Parent.ArtifactConsumption.Drives[0].DriveID == "caller-edit" {
		t.Fatal("source parent aliased")
	}
}
