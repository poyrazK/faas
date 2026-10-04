package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) operationPinsLocked(op Operation) {
	if prior := m.revisionPins[op.DeploymentID]; prior.Before(op.ExpiresAt) {
		m.revisionPins[op.DeploymentID] = op.ExpiresAt
	}
	if release, exists := m.projectReleaseSets[op.ReleaseID]; exists && !release.Active && (release.ExpiresAt == nil || release.ExpiresAt.Before(op.ExpiresAt)) {
		expires := op.ExpiresAt
		release.ExpiresAt = &expires
		m.projectReleaseSets[op.ReleaseID] = release
	}
}

func (m *MemStore) operationReleaseExpiryLocked(releaseID string, expiry time.Time) time.Time {
	if m.operationData == nil {
		return expiry
	}
	for _, op := range m.operationData.operations {
		if op.ReleaseID == releaseID && expiry.Before(op.ExpiresAt) {
			expiry = op.ExpiresAt
		}
	}
	return expiry
}

func (m *MemStore) deploymentRevisionRetainedLocked(id string) bool {
	return m.revisionPins[id].After(time.Now())
}

func operationPinsTx(ctx context.Context, tx pgx.Tx, op Operation) error {
	q := sqlc.New()
	dep, _ := operationUUID(op.DeploymentID)
	app, _ := operationUUID(op.AppID)
	expires := pgtype.Timestamptz{Time: op.ExpiresAt, Valid: true}
	if err := q.PinCustomerOperationDeployment(ctx, tx, sqlc.PinCustomerOperationDeploymentParams{DeploymentID: dep, AppID: app, ExpiresAt: expires}); err != nil {
		return err
	}
	if op.ReleaseID != "" {
		release, err := operationUUID(op.ReleaseID)
		if err != nil {
			return err
		}
		return q.PinCustomerOperationRelease(ctx, tx, sqlc.PinCustomerOperationReleaseParams{ID: release, ExpiresAt: expires})
	}
	return nil
}
