package state

// adr: 435. Retained signed claims require new verification to renew approval.

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BuildExportPublicationHistoryStore = (*MemStore)(nil)
var _ BuildExportPublicationHistoryStore = (*PgStore)(nil)

func (m *MemStore) GetLatestBuildExportPublication(ctx context.Context, accountID, appID, depID, buildID string) (BuildExportPublication, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, buildID) {
		return BuildExportPublication{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BuildExportPublication{}, err
	}
	app, found, _, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) {
		return BuildExportPublication{}, ErrNotFound
	}
	value := m.latestBuildExportPublicationLocked(accountID, appID, depID, buildID)
	if value.ID == "" || value.Input.Claims.OrgID != registryCanonicalOrg(app.OrgID) {
		return BuildExportPublication{}, ErrNotFound
	}
	if err := validateBuildExportPublication(value); err != nil {
		return BuildExportPublication{}, err
	}
	value.Input.Proof = value.Input.Proof.Clone()
	return value, nil
}

func (s *PgStore) GetLatestBuildExportPublication(ctx context.Context, accountID, appID, depID, buildID string) (BuildExportPublication, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, buildID) {
		return BuildExportPublication{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetLatestOwnedBuildExportPublication(ctx, s.pool, sqlc.GetLatestOwnedBuildExportPublicationParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), BuildID: mustPgUUID(buildID)})
	if err != nil {
		return BuildExportPublication{}, buildExportError(err)
	}
	return buildExportPublicationRow(row)
}
