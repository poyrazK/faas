package state

// adr: 595. Restore authority uses the same current native input transaction.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func checkStandardSnapshotRestoreTx(ctx context.Context, tx sqlc.DBTX, binding runtimeadmission.Binding, capture InstanceApplicationStandardAdmission, now time.Time, receipt *runtimeadmission.Receipt) error {
	if binding.SnapshotCaptureToken == "" {
		return nil
	}
	row, err := sqlc.New().LockApplicationStandardSnapshotRestore(ctx, tx, sqlc.LockApplicationStandardSnapshotRestoreParams{Token: mustPgUUID(binding.SnapshotCaptureToken), AccountID: mustPgUUID(binding.AccountID), AppID: mustPgUUID(binding.AppID), DeploymentID: mustPgUUID(binding.DeploymentID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return fmt.Errorf("lock snapshot restore catalog: %w", standardRuntimeQualificationReadError(err))
	}
	r, err := decodeStandardSnapshotRecord(sqlc.GetApplicationStandardSnapshotCaptureRow{ExpectedState: row.ExpectedState, GrantData: row.GrantData, Acknowledgment: row.Acknowledgment, CreatedAt: row.CreatedAt, ReceivedAt: row.ReceivedAt})
	if err != nil {
		return ErrApplicationStandardRuntimeStale
	}
	r.inputs = append([]byte(nil), row.InputSnapshot...)
	snap := Snapshot{DeploymentID: row.DeploymentID, FCVersion: row.FcVersion, StorageKey: row.StorageKey, MemBytes: row.MemBytes, DiskBytes: row.DiskBytes, Tier: row.Tier, ApplicationStandardCaptureToken: binding.SnapshotCaptureToken}
	if err := checkStandardSnapshotRestoreBinding(binding, capture, r, snap, now); err != nil {
		return err
	}
	return checkStandardSnapshotReceipt(receipt, r, now)
}
