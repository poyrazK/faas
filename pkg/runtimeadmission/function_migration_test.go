package runtimeadmission

// adr: 1546

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func functionMigrationGrantFixture(t *testing.T) FunctionMigrationGrant {
	t.Helper()
	base, _ := snapshotGrantFixture(t)
	now := time.Now()
	return FunctionMigrationGrant{Version: FunctionMigrationGrantVersion, Token: base.Token, CaptureToken: base.Token,
		Purpose: FunctionMigrationCapture, Parent: base.Parent.Clone(), SourceWakeID: uuid.NewString(),
		LeaseToken: FunctionMigrationLeasePrefix + uuid.NewString(), LeaseCreatedAtUnixNano: now.Add(-time.Minute).UnixNano(),
		LeaseExpiresAtUnixNano: now.Add(2 * time.Minute).UnixNano(), SourceStartedAtUnixNano: base.SourceStartedAtUnixNano,
		MemoryKey:       strings.Replace(base.MemoryKey, "/captures/", "/warm/captures/", 1),
		VMStateKey:      strings.Replace(base.VMStateKey, "/captures/", "/warm/captures/", 1),
		PrivateDriveKey: strings.Replace(base.PrivateDriveKey, "/captures/", "/warm/captures/", 1), FCVersion: base.FCVersion,
		IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
}

func TestFunctionMigrationGrantFreshPurposeAndLeaseExpiry(t *testing.T) {
	grant := functionMigrationGrantFixture(t)
	if err := grant.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	recovery := grant.Clone()
	recovery.Token, recovery.Purpose = uuid.NewString(), FunctionMigrationRecover
	recovery.LeaseExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
	if err := recovery.Validate(time.Now()); err != nil || !recovery.SameSource(grant) || recovery.Equal(grant) {
		t.Fatal("fresh recovery must remain possible after lease expiry", err)
	}
	if err := recovery.Validate(time.Unix(0, recovery.ExpiresAtUnixNano)); !errors.Is(err, ErrExpired) {
		t.Fatal("recovery permission did not expire", err)
	}
	copy := recovery.Clone()
	copy.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
	if copy.Equal(recovery) || copy.SameSource(grant) || recovery.Parent.ArtifactConsumption.Drives[0].DriveID == "caller-edit" {
		t.Fatal("permission aliases serving history")
	}
}

func TestFunctionMigrationGrantRejectsWrongScope(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*FunctionMigrationGrant)
	}{
		{"version", func(g *FunctionMigrationGrant) { g.Version++ }},
		{"purpose", func(g *FunctionMigrationGrant) { g.Purpose = "warm" }},
		{"capture nonce", func(g *FunctionMigrationGrant) { g.Token = uuid.NewString() }},
		{"recovery nonce", func(g *FunctionMigrationGrant) { g.Purpose = FunctionMigrationRecover }},
		{"wake", func(g *FunctionMigrationGrant) { g.SourceWakeID = "" }},
		{"lease namespace", func(g *FunctionMigrationGrant) { g.LeaseToken = uuid.NewString() }},
		{"lease nonce", func(g *FunctionMigrationGrant) { g.LeaseToken = FunctionMigrationLeasePrefix + "wrong" }},
		{"lease clock", func(g *FunctionMigrationGrant) { g.LeaseExpiresAtUnixNano = g.LeaseCreatedAtUnixNano }},
		{"lease expiry", func(g *FunctionMigrationGrant) { g.ExpiresAtUnixNano = g.LeaseExpiresAtUnixNano + 1 }},
		{"source clock", func(g *FunctionMigrationGrant) { g.SourceStartedAtUnixNano = 0 }},
		{"different drive", func(g *FunctionMigrationGrant) { g.PrivateDriveKey = "other" }},
		{"old capture namespace", func(g *FunctionMigrationGrant) { g.MemoryKey = strings.Replace(g.MemoryKey, "/warm/", "/", 1) }},
		{"paused parent", func(g *FunctionMigrationGrant) { g.Parent.Paused = true }},
		{"unbounded lifetime", func(g *FunctionMigrationGrant) {
			g.ExpiresAtUnixNano = g.IssuedAtUnixNano + int64(api.ApplicationStandardRuntimeAdmissionTTL) + 1
		}},
		{"FC path", func(g *FunctionMigrationGrant) { g.FCVersion = "../1.12.1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			grant := functionMigrationGrantFixture(t)
			test.edit(&grant)
			if err := grant.Validate(time.Now()); err == nil {
				t.Fatal("accepted a permission with altered scope")
			}
		})
	}
}
