package runtimeadmission

// adr: 1546. Serving history never substitutes for current lifecycle permission.

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	FunctionMigrationGrantVersion = 1
	FunctionMigrationCapture      = "capture"
	FunctionMigrationRecover      = "recover"
	FunctionMigrationLeasePrefix  = "function-v1:"
)

type FunctionMigrationGrant struct {
	Version                 uint32  `json:"version"`
	Token                   string  `json:"token"`
	Purpose                 string  `json:"purpose"`
	Parent                  Receipt `json:"parent"`
	CaptureToken            string  `json:"capture_token"`
	SourceWakeID            string  `json:"source_wake_id"`
	LeaseToken              string  `json:"lease_token"`
	LeaseCreatedAtUnixNano  int64   `json:"lease_created_at_unix_nano"`
	LeaseExpiresAtUnixNano  int64   `json:"lease_expires_at_unix_nano"`
	SourceStartedAtUnixNano int64   `json:"source_started_at_unix_nano"`
	MemoryKey               string  `json:"memory_key"`
	VMStateKey              string  `json:"vmstate_key"`
	PrivateDriveKey         string  `json:"private_drive_key"`
	FCVersion               string  `json:"fc_version"`
	IssuedAtUnixNano        int64   `json:"issued_at_unix_nano"`
	ExpiresAtUnixNano       int64   `json:"expires_at_unix_nano"`
}

func (g FunctionMigrationGrant) Clone() FunctionMigrationGrant {
	g.Parent = g.Parent.Clone()
	return g
}

func (g FunctionMigrationGrant) Equal(other FunctionMigrationGrant) bool {
	return g.sameFields(other) && g.Parent.Equal(other.Parent)
}

func (g FunctionMigrationGrant) sameFields(other FunctionMigrationGrant) bool {
	return g.Version == other.Version && g.Token == other.Token && g.Purpose == other.Purpose &&
		g.CaptureToken == other.CaptureToken && g.SourceWakeID == other.SourceWakeID && g.LeaseToken == other.LeaseToken &&
		g.LeaseCreatedAtUnixNano == other.LeaseCreatedAtUnixNano && g.LeaseExpiresAtUnixNano == other.LeaseExpiresAtUnixNano &&
		g.SourceStartedAtUnixNano == other.SourceStartedAtUnixNano && g.MemoryKey == other.MemoryKey &&
		g.VMStateKey == other.VMStateKey && g.PrivateDriveKey == other.PrivateDriveKey && g.FCVersion == other.FCVersion &&
		g.IssuedAtUnixNano == other.IssuedAtUnixNano && g.ExpiresAtUnixNano == other.ExpiresAtUnixNano
}

// SameSource permits only the new purpose, nonce, deadline and observed lease
// expiry to differ. A lease renewal never changes the original physical owner.
func (g FunctionMigrationGrant) SameSource(capture FunctionMigrationGrant) bool {
	copy := g
	copy.Token, copy.Purpose = capture.Token, capture.Purpose
	copy.IssuedAtUnixNano, copy.ExpiresAtUnixNano = capture.IssuedAtUnixNano, capture.ExpiresAtUnixNano
	copy.LeaseExpiresAtUnixNano = capture.LeaseExpiresAtUnixNano
	return copy.Equal(capture)
}

func (g FunctionMigrationGrant) Validate(now time.Time) error {
	if g.Version != FunctionMigrationGrantVersion || !canonicalUUID(g.Token) || !canonicalUUID(g.CaptureToken) ||
		!canonicalUUID(g.SourceWakeID) || !strings.HasPrefix(g.LeaseToken, FunctionMigrationLeasePrefix) ||
		!canonicalUUID(strings.TrimPrefix(g.LeaseToken, FunctionMigrationLeasePrefix)) || CheckSnapshotParent(g.Parent) != nil {
		return ErrInvalid
	}
	if g.Purpose != FunctionMigrationCapture && g.Purpose != FunctionMigrationRecover ||
		(g.Purpose == FunctionMigrationCapture) != (g.Token == g.CaptureToken) {
		return ErrInvalid
	}
	if CheckSnapshotCaptureKeys(g.Parent.Binding.DeploymentID, g.MemoryKey, g.VMStateKey, g.PrivateDriveKey) != nil ||
		!strings.HasSuffix(g.MemoryKey, "/warm/captures/"+g.CaptureToken+"/v2/mem") || !freshSnapshotParentNamespace(g.Parent, g.MemoryKey) {
		return ErrInvalid
	}
	if g.FCVersion == "" || len(g.FCVersion) > api.ApplicationStandardSnapshotMaxFCVersionBytes ||
		strings.TrimSpace(g.FCVersion) != g.FCVersion || strings.ContainsAny(g.FCVersion, "\x00\r\n\t /\\") {
		return ErrInvalid
	}
	if g.LeaseCreatedAtUnixNano <= 0 || g.LeaseExpiresAtUnixNano <= g.LeaseCreatedAtUnixNano || g.SourceStartedAtUnixNano <= 0 ||
		g.IssuedAtUnixNano <= 0 || g.ExpiresAtUnixNano <= g.IssuedAtUnixNano ||
		g.ExpiresAtUnixNano-g.IssuedAtUnixNano > int64(api.ApplicationStandardRuntimeAdmissionTTL) ||
		g.IssuedAtUnixNano > now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano() ||
		g.SourceStartedAtUnixNano > g.IssuedAtUnixNano+int64(api.ApplicationStandardRuntimeAdmissionClockSkew) ||
		g.LeaseCreatedAtUnixNano > g.IssuedAtUnixNano+int64(api.ApplicationStandardRuntimeAdmissionClockSkew) ||
		g.Parent.CompletedAtUnixNano > g.IssuedAtUnixNano+int64(api.ApplicationStandardRuntimeAdmissionClockSkew) {
		return ErrInvalid
	}
	if g.Purpose == FunctionMigrationCapture && g.ExpiresAtUnixNano > g.LeaseExpiresAtUnixNano {
		return ErrInvalid
	}
	if !now.Before(time.Unix(0, g.ExpiresAtUnixNano)) {
		return ErrExpired
	}
	return nil
}
