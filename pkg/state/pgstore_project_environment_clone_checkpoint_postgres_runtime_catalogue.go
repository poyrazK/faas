package state

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// Authenticate historical runtime authority without calling a native fork reader
// (which itself authenticates this catalogue). Current health, SQL closure and
// retained source evidence are independently required by worker coordinators.
func checkpointPostgresRuntimeCatalogueTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, fork ProjectEnvironmentClonePostgresSnapshotRestore, source ProjectEnvironmentClonePostgresBinding, point time.Time) (bool, string, error) {
	if len(op.Resources) == 0 || op.Resources[0].Kind != "data_checkpoint" {
		return false, "", nil
	}
	runtime, err := readCheckpointPostgresRuntimeTx(ctx, tx, op, fork.SourceDatabaseID)
	if errors.Is(err, ErrNotFound) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if runtime.State != "ready" {
		return false, "", nil
	}
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return false, "", err
	}
	snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, fork.SourceDatabaseID)
	if err != nil {
		return false, "", err
	}
	isolation, err := readCheckpointPostgresIsolationTx(ctx, tx, op, fork.SourceDatabaseID)
	if err != nil {
		return false, "", err
	}
	verification, err := readCheckpointPostgresVerificationTx(ctx, tx, op, fork.SourceDatabaseID)
	if err != nil {
		return false, "", err
	}
	expected := ProjectEnvironmentCloneCheckpointPostgresIsolationPlan{OperationID: op.ID, AccountID: op.AccountID, SourceDatabaseID: fork.SourceDatabaseID, OwnerID: fork.TargetOwnerID, NativeResourceID: fork.TargetProviderResourceID, BackendID: fork.BackendID, BackendFingerprint: fork.BackendFingerprint, CheckpointID: release.Receipt.CheckpointID, ProviderSnapshotID: snapshot.ProviderSnapshotID, SourceDataResourceID: source.DataResourceID, CapturePoint: point.UTC(), SnapshotCreatedAt: snapshot.SnapshotCreatedAt.UTC(), TargetCreatedAt: fork.TargetCreatedAt.UTC()}
	if release.State != "released" || isolation.State != "adopted" || verification.State != "verified" || !reflect.DeepEqual(isolation.Plan, expected) || !reflect.DeepEqual(verification.Plan.Isolation, expected) || verification.Plan.IsolationSHA256 != isolation.ObservationSHA256 || verification.Plan.MaxReadBytesPerSide != api.PostgresCopyArchiveMaxBytes || !reflect.DeepEqual(runtime.Plan.Verification, verification.Plan) || runtime.Plan.VerificationSHA256 != verification.ObservationSHA256 || runtime.Plan.Spec != checkpointPostgresRuntimeSpec(source) {
		return false, "", ErrConflict
	}
	return true, runtime.Observation.DataResourceID, nil
}
