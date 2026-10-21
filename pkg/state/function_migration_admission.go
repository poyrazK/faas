package state

// adr: 20261018030614. Permission issuance reuses the complete native input fences.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type FunctionMigrationCaptureRequest struct {
	Source                           FunctionMigrationSource
	Parent                           runtimeadmission.Receipt
	MemoryKey, VMStateKey, FCVersion string
}

// vmmd issues/checks these permissions within the authenticated source RPC.
// No method in this interface writes the instance ownership row.
type FunctionMigrationAdmissionStore interface {
	IssueFunctionMigrationCapture(context.Context, FunctionMigrationCaptureRequest) (runtimeadmission.FunctionMigrationGrant, error)
	CheckFunctionMigrationCapture(context.Context, runtimeadmission.FunctionMigrationGrant) error
	IssueFunctionMigrationRecovery(context.Context, string, string) (runtimeadmission.FunctionMigrationGrant, error)
	CheckFunctionMigrationRecovery(context.Context, runtimeadmission.FunctionMigrationGrant) error
}

type functionMigrationPermissions struct {
	capture  runtimeadmission.FunctionMigrationGrant
	recovery *runtimeadmission.FunctionMigrationGrant
	result   *runtimeadmission.SnapshotCapture
}

func decodeFunctionMigrationGrant(raw []byte, purpose string) (runtimeadmission.FunctionMigrationGrant, error) {
	var grant runtimeadmission.FunctionMigrationGrant
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&grant); err != nil {
		return grant, ErrApplicationStandardRuntimeStale
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || grant.Purpose != purpose || grant.Validate(time.Unix(0, grant.IssuedAtUnixNano)) != nil {
		return grant, ErrApplicationStandardRuntimeStale
	}
	return grant, nil
}

func newFunctionMigrationCapture(req FunctionMigrationCaptureRequest, lease MigrationLease, parent runtimeadmission.Receipt, started int64, now, artifactDeadline time.Time) (runtimeadmission.FunctionMigrationGrant, error) {
	token, ok := SnapshotCaptureToken(req.MemoryKey)
	if !ok || !req.Parent.Equal(parent) || validateFunctionMigrationSource(req.Source) != nil ||
		req.Source.InstanceID != parent.Binding.InstanceID || req.Source.SourceNodeID != parent.Binding.NodeID {
		return runtimeadmission.FunctionMigrationGrant{}, ErrApplicationStandardRuntimeStale
	}
	expires, err := standardNativeGrantExpiry(now, artifactDeadline)
	if err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	if lease.LeaseExpiresAt.Before(expires) {
		expires = lease.LeaseExpiresAt
	}
	g := runtimeadmission.FunctionMigrationGrant{
		Version: runtimeadmission.FunctionMigrationGrantVersion, Token: token, CaptureToken: token,
		Purpose: runtimeadmission.FunctionMigrationCapture, Parent: parent.Clone(),
		SourceWakeID: req.Source.SourceWakeID, LeaseToken: lease.LeaseToken,
		LeaseCreatedAtUnixNano: lease.CreatedAt.UnixNano(), LeaseExpiresAtUnixNano: lease.LeaseExpiresAt.UnixNano(),
		SourceStartedAtUnixNano: started, MemoryKey: req.MemoryKey, VMStateKey: req.VMStateKey,
		PrivateDriveKey: SnapshotDriveKey(Snapshot{StorageKey: req.MemoryKey}), FCVersion: req.FCVersion,
		IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: expires.UnixNano(),
	}
	return g, g.Validate(now)
}

func newFunctionMigrationRecovery(capture runtimeadmission.FunctionMigrationGrant, lease MigrationLease, now, artifactDeadline time.Time) (runtimeadmission.FunctionMigrationGrant, error) {
	expires, err := standardNativeGrantExpiry(now, artifactDeadline)
	if err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	g := capture.Clone()
	g.Token, g.Purpose = uuid.NewString(), runtimeadmission.FunctionMigrationRecover
	g.LeaseExpiresAtUnixNano = lease.LeaseExpiresAt.UnixNano()
	g.IssuedAtUnixNano, g.ExpiresAtUnixNano = now.UnixNano(), expires.UnixNano()
	return g, g.Validate(now)
}

func functionMigrationPermissionOwner(ins Instance, source FunctionMigrationSource, lease MigrationLease, recovering bool) error {
	if ins.ID != source.InstanceID || ins.NodeID != source.SourceNodeID || ins.WakeID != source.SourceWakeID ||
		lease.InstanceID != source.InstanceID || lease.SourceNodeID != source.SourceNodeID || lease.LeaseToken != source.LeaseToken {
		return ErrApplicationStandardRuntimeStale
	}
	if recovering {
		if source.CaptureToken == "" || source.Phase != FunctionMigrationRecovering || ins.State != string(StateMigrating) ||
			ins.LeaseToken != FunctionMigrationRollbackToken(source.LeaseToken) || lease.Pending {
			return ErrConflict
		}
	} else if ins.State != string(StateRunning) || ins.LeaseToken != "" || !lease.Pending {
		return ErrConflict
	}
	return nil
}

func checkFunctionMigrationPermission(g runtimeadmission.FunctionMigrationGrant, parent runtimeadmission.Receipt, started int64, lease MigrationLease, now, deadline time.Time) error {
	if g.Validate(now) != nil || !g.Parent.Equal(parent) || g.SourceStartedAtUnixNano != started ||
		g.LeaseCreatedAtUnixNano != lease.CreatedAt.UnixNano() || g.LeaseExpiresAtUnixNano != lease.LeaseExpiresAt.UnixNano() ||
		g.MemoryKey != lease.MemStorageKey || g.VMStateKey != lease.VMStateStorageKey ||
		!standardNativeGrantWithinArtifactLease(g.ExpiresAtUnixNano, deadline) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
