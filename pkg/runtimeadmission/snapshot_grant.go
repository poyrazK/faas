package runtimeadmission

// adr: 435. Fresh capture authority is distinct from historical byte lineage.

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const SnapshotGrantVersion = 1

type SnapshotGrant struct {
	Version                 uint32  `json:"version"`
	Token                   string  `json:"token"`
	Parent                  Receipt `json:"parent"`
	MemoryKey               string  `json:"memory_key"`
	VMStateKey              string  `json:"vmstate_key"`
	PrivateDriveKey         string  `json:"private_drive_key"`
	FCVersion               string  `json:"fc_version"`
	Mode                    string  `json:"mode"`
	BeforeCheckpoint        bool    `json:"before_checkpoint"`
	SourceStartedAtUnixNano int64   `json:"source_started_at_unix_nano"`
	IssuedAtUnixNano        int64   `json:"issued_at_unix_nano"`
	ExpiresAtUnixNano       int64   `json:"expires_at_unix_nano"`
}

func (g SnapshotGrant) Clone() SnapshotGrant {
	g.Parent = g.Parent.Clone()
	return g
}

func (g SnapshotGrant) Equal(other SnapshotGrant) bool {
	return g.Version == other.Version && g.Token == other.Token && g.Parent.Equal(other.Parent) && g.MemoryKey == other.MemoryKey && g.VMStateKey == other.VMStateKey && g.PrivateDriveKey == other.PrivateDriveKey && g.FCVersion == other.FCVersion && g.Mode == other.Mode && g.BeforeCheckpoint == other.BeforeCheckpoint && g.SourceStartedAtUnixNano == other.SourceStartedAtUnixNano && g.IssuedAtUnixNano == other.IssuedAtUnixNano && g.ExpiresAtUnixNano == other.ExpiresAtUnixNano
}

func (g SnapshotGrant) Validate(now time.Time) error {
	if g.Version != SnapshotGrantVersion || !canonicalUUID(g.Token) || CheckSnapshotParent(g.Parent) != nil || !freshSnapshotParentNamespace(g.Parent, g.MemoryKey) || g.Parent.CompletedAtUnixNano > now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano() || g.SourceStartedAtUnixNano <= 0 {
		return ErrInvalid
	}
	if CheckSnapshotCaptureKeys(g.Parent.Binding.DeploymentID, g.MemoryKey, g.VMStateKey, g.PrivateDriveKey) != nil || !strings.HasSuffix(g.MemoryKey, "/captures/"+g.Token+"/v2/mem") || !validSnapshotGrantMode(g) {
		return ErrInvalid
	}
	if g.FCVersion == "" || len(g.FCVersion) > api.ApplicationStandardSnapshotMaxFCVersionBytes || strings.TrimSpace(g.FCVersion) != g.FCVersion || strings.ContainsAny(g.FCVersion, "\x00\r\n\t /\\") {
		return ErrInvalid
	}
	issued, expires := time.Unix(0, g.IssuedAtUnixNano), time.Unix(0, g.ExpiresAtUnixNano)
	if g.IssuedAtUnixNano <= 0 || !expires.After(issued) || expires.Sub(issued) > api.ApplicationStandardRuntimeAdmissionTTL || issued.After(now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew)) || g.SourceStartedAtUnixNano > g.IssuedAtUnixNano+int64(api.ApplicationStandardRuntimeAdmissionClockSkew) {
		return ErrInvalid
	}
	if !now.Before(expires) {
		return ErrExpired
	}
	return nil
}

func validSnapshotGrantMode(g SnapshotGrant) bool {
	warm := strings.Contains(g.MemoryKey, "/warm/captures/")
	return g.Mode == "warm" && warm && !g.BeforeCheckpoint || g.Mode == "park" && !warm || g.Mode == "migration" && !warm && !g.BeforeCheckpoint
}

type SnapshotAcknowledgment struct {
	Grant               SnapshotGrant   `json:"grant"`
	Capture             SnapshotCapture `json:"capture"`
	CompletedAtUnixNano int64           `json:"completed_at_unix_nano"`
}

func (a SnapshotAcknowledgment) Clone() SnapshotAcknowledgment {
	a.Grant, a.Capture = a.Grant.Clone(), a.Capture.Clone()
	return a
}

func (a SnapshotAcknowledgment) Equal(other SnapshotAcknowledgment) bool {
	return a.Grant.Equal(other.Grant) && a.Capture.Equal(other.Capture) && a.CompletedAtUnixNano == other.CompletedAtUnixNano
}

func (a SnapshotAcknowledgment) Check(grant SnapshotGrant, now time.Time) error {
	if err := grant.Validate(now); err != nil {
		return err
	}
	if !a.Grant.Equal(grant) || a.Capture.Check(now) != nil || !a.Capture.Parent.Equal(grant.Parent) || a.Capture.Memory.StorageKey != grant.MemoryKey || a.Capture.VMState.StorageKey != grant.VMStateKey || a.Capture.PrivateDrive.StorageKey != grant.PrivateDriveKey {
		return ErrInvalid
	}
	if a.Capture.CapturedAtUnixNano < grant.IssuedAtUnixNano-int64(api.ApplicationStandardRuntimeAdmissionClockSkew) || a.CompletedAtUnixNano < a.Capture.CapturedAtUnixNano || a.CompletedAtUnixNano >= grant.ExpiresAtUnixNano || time.Unix(0, a.CompletedAtUnixNano).After(now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew)) {
		return ErrInvalid
	}
	return nil
}
