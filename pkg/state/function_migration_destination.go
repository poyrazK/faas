package state

// adr: 1577. Destination readiness and ownership are separate durable phases.

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

const (
	FunctionDestinationIssued    = "issued"
	FunctionDestinationReady     = "ready"
	FunctionDestinationCommitted = "committed"
	FunctionDestinationRetired   = "retired"
)

type FunctionMigrationDestination struct {
	Grant   runtimeadmission.FunctionMigrationDestinationGrant
	Receipt *runtimeadmission.Receipt
	Phase   string
}

func (d FunctionMigrationDestination) Clone() FunctionMigrationDestination {
	d.Grant = d.Grant.Clone()
	if d.Receipt != nil {
		r := d.Receipt.Clone()
		d.Receipt = &r
	}
	return d
}

type FunctionMigrationDestinationRequest struct {
	InstanceID, LeaseToken, WakeID, NativeInputHash string
	Identity                                        runtimeadmission.Identity
}

// vmmd authors physical evidence; only schedd publishes instance ownership.
type FunctionMigrationDestinationStore interface {
	GetFunctionMigrationCapture(context.Context, string, string) (runtimeadmission.SnapshotRestoreEvidence, error)
	RecordFunctionMigrationCapture(context.Context, runtimeadmission.FunctionMigrationGrant, runtimeadmission.SnapshotCapture) error
	IssueFunctionMigrationDestination(context.Context, FunctionMigrationDestinationRequest) (runtimeadmission.FunctionMigrationDestinationGrant, error)
	GetFunctionMigrationDestination(context.Context, string, string) (FunctionMigrationDestination, error)
	CheckFunctionMigrationDestination(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error
	ReadyFunctionMigrationDestination(context.Context, runtimeadmission.FunctionMigrationDestinationGrant, runtimeadmission.Receipt) error
	CheckFunctionMigrationDestinationRetirement(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error
	RetireFunctionMigrationDestination(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error
	CheckFunctionMigrationDestinationCommitted(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error
}

func checkFunctionMigrationCaptureResult(g runtimeadmission.FunctionMigrationGrant, c runtimeadmission.SnapshotCapture, now time.Time) error {
	if g.Purpose != runtimeadmission.FunctionMigrationCapture || c.Check(now) != nil || !c.Parent.Equal(g.Parent) ||
		c.Memory.StorageKey != g.MemoryKey || c.VMState.StorageKey != g.VMStateKey || c.PrivateDrive.StorageKey != g.PrivateDriveKey ||
		c.CapturedAtUnixNano < g.IssuedAtUnixNano || c.CapturedAtUnixNano >= g.ExpiresAtUnixNano {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func newFunctionMigrationDestination(req FunctionMigrationDestinationRequest, capture runtimeadmission.FunctionMigrationGrant, result runtimeadmission.SnapshotCapture,
	lease MigrationLease, inputHash string, now, deadline time.Time) (runtimeadmission.FunctionMigrationDestinationGrant, error) {
	if req.Identity.Validate() != nil || req.Identity.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion ||
		req.Identity.NodeID == capture.Parent.Binding.NodeID || !runtimeadmission.ValidHash(req.NativeInputHash) ||
		checkFunctionMigrationCaptureResult(capture, result, now) != nil {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, ErrApplicationStandardRuntimeStale
	}
	expires, err := standardNativeGrantExpiry(now, deadline)
	if err != nil {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, err
	}
	if expires.After(lease.LeaseExpiresAt) {
		expires = lease.LeaseExpiresAt
	}
	g := runtimeadmission.FunctionMigrationDestinationGrant{Version: 1, Source: capture.Clone(), WakeID: req.WakeID,
		Restore: runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: capture.CaptureToken, FCVersion: capture.FCVersion, Capture: result.Clone()},
		Binding: capture.Parent.Binding}
	b := &g.Binding
	b.Token, b.NodeID, b.Incarnation = uuid.NewString(), req.Identity.NodeID, req.Identity.Incarnation
	b.CapturedInputHash, b.PayloadHash = inputHash, req.NativeInputHash
	b.SnapshotCaptureToken = capture.CaptureToken
	b.SnapshotEvidenceHash, err = g.Restore.Hash()
	if err != nil {
		return g, err
	}
	b.IssuedAtUnixNano, b.ExpiresAtUnixNano = now.UnixNano(), expires.UnixNano()
	return g, g.Validate(now)
}

func functionDestinationOwner(ins Instance, source FunctionMigrationSource, lease MigrationLease) error {
	if source.CaptureToken == "" || source.Phase != FunctionMigrationPrepared || lease.Pending || !time.Now().Before(lease.LeaseExpiresAt) ||
		ins.State != string(StateMigrating) || ins.NodeID != source.SourceNodeID || ins.WakeID != source.SourceWakeID || ins.LeaseToken != source.LeaseToken ||
		lease.InstanceID != ins.ID || lease.SourceNodeID != ins.NodeID || lease.LeaseToken != source.LeaseToken {
		return ErrConflict
	}
	return nil
}

func checkFunctionDestinationRuntime(input RuntimeConfigMigration, d FunctionMigrationDestination) error {
	if validateRuntimeConfigMigration(input) != nil || d.Phase != FunctionDestinationReady || d.Receipt == nil ||
		input.ExpectedWakeID != d.Grant.Source.SourceWakeID || input.WakeID != d.Grant.WakeID || input.Inputs == nil ||
		input.Netns != d.Receipt.Netns || input.HostIP != d.Receipt.HostIP || input.GuestUID != int(d.Receipt.LeaseUID) ||
		d.Grant.CheckReceipt(*d.Receipt, time.Now()) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

// Resolve a lost ownership ACK without the legacy transition to PARKED. The
// source fence serializes with the destination CAS and never resumes either VM.
func resolveFunctionDestinationCommit(ctx context.Context, s interface {
	FunctionMigrationSourceStore
	FunctionMigrationDestinationStore
}, a MigrationCommitAttempt) (MigrationCommitResolution, error) {
	if err := validateMigrationCommitAttempt(a); err != nil {
		return MigrationCommitRetained, err
	}
	if d, err := s.GetFunctionMigrationDestination(ctx, a.InstanceID, a.LeaseToken); err == nil && d.Phase == FunctionDestinationCommitted &&
		d.Grant.Binding.NodeID == a.DestinationNodeID && d.Grant.Source.Parent.Binding.NodeID == a.SourceNodeID && d.Grant.Source.SourceWakeID == a.SourceWakeID && d.Grant.WakeID == a.DestinationWakeID {
		if err := s.CheckFunctionMigrationDestinationCommitted(ctx, d.Grant); err != nil {
			return MigrationCommitRetained, err
		}
		return MigrationCommitRecovered, nil
	}
	resolution, err := s.FenceFunctionMigrationSource(ctx, a.InstanceID, a.LeaseToken)
	if err != nil {
		return MigrationCommitRetained, err
	}
	if resolution == MigrationCommitAborted {
		return resolution, nil
	}
	d, err := s.GetFunctionMigrationDestination(ctx, a.InstanceID, a.LeaseToken)
	if err != nil {
		return MigrationCommitRetained, err
	}
	if d.Phase == FunctionDestinationCommitted && d.Grant.Binding.NodeID == a.DestinationNodeID && d.Grant.Source.Parent.Binding.NodeID == a.SourceNodeID &&
		d.Grant.Source.SourceWakeID == a.SourceWakeID && d.Grant.WakeID == a.DestinationWakeID {
		if err := s.CheckFunctionMigrationDestinationCommitted(ctx, d.Grant); err != nil {
			return MigrationCommitRetained, err
		}
		return MigrationCommitRecovered, nil
	}
	return MigrationCommitRetained, ErrMigrationCommitUnresolved
}
