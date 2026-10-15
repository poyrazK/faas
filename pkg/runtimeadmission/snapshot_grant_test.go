package runtimeadmission

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func snapshotGrantFixture(t *testing.T) (SnapshotGrant, SnapshotAcknowledgment) {
	t.Helper()
	c := snapshotCaptureFixture(t)
	token := strings.Split(c.Memory.StorageKey, "/")[3]
	now := time.Now()
	g := SnapshotGrant{Version: SnapshotGrantVersion, Token: token, Parent: c.Parent.Clone(), MemoryKey: c.Memory.StorageKey, VMStateKey: c.VMState.StorageKey, PrivateDriveKey: c.PrivateDrive.StorageKey, FCVersion: "1.12.1", Mode: "park", SourceStartedAtUnixNano: c.Parent.CompletedAtUnixNano, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	c.CapturedAtUnixNano = now.UnixNano()
	return g, SnapshotAcknowledgment{Grant: g.Clone(), Capture: c, CompletedAtUnixNano: now.UnixNano()}
}

func TestSnapshotGrantFreshAuthorityAndOwnedHistory(t *testing.T) {
	g, a := snapshotGrantFixture(t)
	if err := a.Check(g, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(time.Now().Add(time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("fresh capture grant failed to expire: %v", err)
	}
	if err := a.Capture.Check(time.Now().Add(time.Hour)); err != nil {
		t.Fatal("grant expiry erased measured lineage", err)
	}
	copy := a.Clone()
	copy.Grant.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-edit"
	copy.Capture.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
	if copy.Equal(a) || a.Grant.Parent.ArtifactConsumption.Drives[0].Source.StorageKey == "caller-edit" || a.Capture.Parent.ArtifactConsumption.Drives[0].DriveID == "caller-edit" {
		t.Fatal("acknowledgment aliases its parents")
	}
}

func TestSnapshotGrantRejectsCrossCaptureAndInvalidAuthority(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*SnapshotGrant)
	}{
		{"different token", func(g *SnapshotGrant) { g.Token = uuid.NewString() }},
		{"wrong mode", func(g *SnapshotGrant) { g.Mode = "warm" }},
		{"migration hook", func(g *SnapshotGrant) { g.Mode = "migration"; g.BeforeCheckpoint = true }},
		{"unmeasured", func(g *SnapshotGrant) { g.Parent.ArtifactConsumption = ArtifactConsumption{} }},
		{"different state key", func(g *SnapshotGrant) { g.VMStateKey += "-other" }},
		{"future source", func(g *SnapshotGrant) { g.SourceStartedAtUnixNano = g.IssuedAtUnixNano + int64(time.Hour) }},
		{"long lease", func(g *SnapshotGrant) {
			g.ExpiresAtUnixNano = g.IssuedAtUnixNano + int64(api.ApplicationStandardRuntimeAdmissionTTL) + 1
		}},
		{"version whitespace", func(g *SnapshotGrant) { g.FCVersion = "1.12.1\n" }},
		{"version path", func(g *SnapshotGrant) { g.FCVersion = "../1.12.1" }},
		{"missing version", func(g *SnapshotGrant) { g.FCVersion = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := snapshotGrantFixture(t)
			tc.edit(&g)
			if g.Validate(time.Now()) == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}

func TestSnapshotAcknowledgmentRejectsUnissuedOrLateFacts(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*SnapshotAcknowledgment)
	}{
		{"different grant", func(a *SnapshotAcknowledgment) { a.Grant.BeforeCheckpoint = true }},
		{"different parent", func(a *SnapshotAcknowledgment) { a.Capture.Parent.Netns = "other" }},
		{"before grant", func(a *SnapshotAcknowledgment) {
			a.Capture.CapturedAtUnixNano = a.Grant.IssuedAtUnixNano - int64(time.Minute)
		}},
		{"completion before capture", func(a *SnapshotAcknowledgment) { a.CompletedAtUnixNano = a.Capture.CapturedAtUnixNano - 1 }},
		{"completion at expiry", func(a *SnapshotAcknowledgment) { a.CompletedAtUnixNano = a.Grant.ExpiresAtUnixNano }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, a := snapshotGrantFixture(t)
			tc.edit(&a)
			if a.Check(g, time.Now()) == nil {
				t.Fatal("invalid acknowledgment accepted")
			}
		})
	}
}
