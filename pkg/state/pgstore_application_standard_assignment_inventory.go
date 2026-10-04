package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardAssignmentInventoryStore = (*PgStore)(nil)

func (s *PgStore) GetApplicationStandardAssignmentRecord(ctx context.Context, orgID, id string) (api.ApplicationStandardAssignment, error) {
	if !validStandardResourceRead(orgID, id) {
		return api.ApplicationStandardAssignment{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetApplicationStandardAssignmentInventory(ctx, s.pool, sqlc.GetApplicationStandardAssignmentInventoryParams{OrgID: mustPgUUID(orgID), AssignmentID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ApplicationStandardAssignment{}, ErrNotFound
	}
	if err != nil {
		return api.ApplicationStandardAssignment{}, fmt.Errorf("read application standard assignment: %w", err)
	}
	return standardAssignmentInventoryRow(row), nil
}

func (s *PgStore) ListApplicationStandardAssignmentRecords(ctx context.Context, orgID, after string, limit int) ([]api.ApplicationStandardAssignment, error) {
	if !validStandardResourcePage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	if after != "" {
		after = canonicalStandardUUID(after)
	}
	rows, err := sqlc.New().ListApplicationStandardAssignmentInventory(ctx, s.pool, sqlc.ListApplicationStandardAssignmentInventoryParams{OrgID: mustPgUUID(orgID), AfterID: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list application standard assignments: %w", err)
	}
	result := []api.ApplicationStandardAssignment{}
	for _, row := range rows {
		result = append(result, standardAssignmentInventoryRow(row))
	}
	return result, nil
}

func standardAssignmentInventoryRow(row sqlc.ApplicationStandardAssignment) api.ApplicationStandardAssignment {
	return api.ApplicationStandardAssignment{ID: pgUUIDString(row.ID), OrgID: pgUUIDString(row.OrgID), Scope: row.Scope, ScopeID: pgUUIDString(row.ScopeID), StandardID: pgUUIDString(row.StandardID), AdmissionVersion: row.AdmissionVersion, Revision: row.Revision, Active: row.Active, CreatedBy: pgUUIDString(row.CreatedBy), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
