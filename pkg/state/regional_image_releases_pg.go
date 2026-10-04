package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalImageReleaseStore = (*PgStore)(nil)

func (s *PgStore) RegionalImageReleaseReceipt(ctx context.Context, cellID, appID, releaseID string) (RegionalImageReleaseReceipt, error) {
	if !validRegionalReleaseIDs(cellID, appID, releaseID) {
		return RegionalImageReleaseReceipt{}, ErrInvalidArgument
	}
	return readRegionalImageRelease(ctx, s.pool, cellID, appID, releaseID, true)
}

func readRegionalImageRelease(ctx context.Context, db sqlc.DBTX, cellID, appID, releaseID string, requireCurrentOwner bool) (RegionalImageReleaseReceipt, error) {
	row, err := (&sqlc.Queries{}).RegionalImageReleaseReceipt(ctx, db, sqlc.RegionalImageReleaseReceiptParams{
		CellID: uuidToPgtype(cellID), AppID: uuidToPgtype(appID), ReleaseID: uuidToPgtype(releaseID), RequireCurrentOwner: requireCurrentOwner,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RegionalImageReleaseReceipt{}, ErrNotFound
	}
	if err != nil {
		return RegionalImageReleaseReceipt{}, fmt.Errorf("state: read regional image release: %w", err)
	}
	return RegionalImageReleaseReceipt{
		CellID: pgUUIDString(row.CellID), AppID: pgUUIDString(row.AppID), ReleaseID: pgUUIDString(row.ReleaseID),
		AccountID: pgUUIDString(row.AccountID), ImageRef: row.ImageRef,
		DeploymentID: pgUUIDString(row.DeploymentID), DeploymentStatus: row.DeploymentStatus, CreatedAt: row.CreatedAt.Time,
	}, nil
}

func (s *PgStore) CreateRegionalImageRelease(ctx context.Context, intent RegionalImageReleaseIntent, d Deployment, activity *OrgActivity) (RegionalImageReleaseResult, error) {
	if err := validateRegionalImageRelease(intent, d); err != nil {
		return RegionalImageReleaseResult{}, err
	}
	if activity != nil {
		copy := *activity
		activity = &copy
	}
	mutation := &regionalImageReleaseMutation{intent: intent}
	created, outboxID, err := s.createDeployment(ctx, d, activity, mutation)
	return RegionalImageReleaseResult{Receipt: mutation.receipt, Deployment: created, ActivityOutboxID: outboxID, Replayed: mutation.replayed}, err
}

func lockRegionalImageRelease(ctx context.Context, tx pgx.Tx, mutation *regionalImageReleaseMutation) error {
	i := mutation.intent
	_, err := (&sqlc.Queries{}).LockRegionalImageReleaseApp(ctx, tx, sqlc.LockRegionalImageReleaseAppParams{
		CellID: uuidToPgtype(i.CellID), AppID: uuidToPgtype(i.AppID), AccountID: uuidToPgtype(i.AccountID), OrgID: uuidToPgtype(i.OrgID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: lock regional release app: %w", err)
	}
	receipt, err := readRegionalImageRelease(ctx, tx, i.CellID, i.AppID, i.ReleaseID, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return mutation.replay(receipt)
}

func insertRegionalImageRelease(ctx context.Context, tx pgx.Tx, mutation *regionalImageReleaseMutation, d Deployment) error {
	i := mutation.intent
	err := (&sqlc.Queries{}).InsertRegionalImageReleaseReceipt(ctx, tx, sqlc.InsertRegionalImageReleaseReceiptParams{
		CellID: uuidToPgtype(i.CellID), AppID: uuidToPgtype(i.AppID), ReleaseID: uuidToPgtype(i.ReleaseID),
		AccountID: uuidToPgtype(i.AccountID), ImageRef: i.ImageRef, DeploymentID: uuidToPgtype(d.ID), CreatedAt: pgtypeTimestamptzFromTime(d.CreatedAt),
	})
	if err != nil {
		return fmt.Errorf("state: insert regional image release receipt: %w", err)
	}
	mutation.accepted(d)
	return nil
}
