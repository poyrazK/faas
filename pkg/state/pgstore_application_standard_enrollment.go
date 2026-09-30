package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardEnrollmentStore = (*PgStore)(nil)

func (s *PgStore) GetApplicationStandardEnrollment(ctx context.Context, orgID, appID string) (ApplicationStandardEnrollment, error) {
	if !validStandardResourceRead(orgID, appID) {
		return ApplicationStandardEnrollment{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetApplicationStandardEnrollment(ctx, s.pool, sqlc.GetApplicationStandardEnrollmentParams{OrgID: mustPgUUID(orgID), AppID: mustPgUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardEnrollment{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardEnrollment{}, fmt.Errorf("read standard enrollment: %w", err)
	}
	value := ApplicationStandardEnrollment{AppID: row.AppID, OrgID: row.OrgID, ProjectID: row.ProjectID, AdditionalLogDestinations: row.AdditionalLogDestinations, EffectiveHash: row.EffectiveHash, DesiredRevision: row.DesiredRevision, PersistedRevision: row.PersistedRevision, ObservedRevision: row.ObservedRevision, State: row.State, ErrorCode: row.ErrorCode, UpdatedAt: row.UpdatedAt.Time}
	for _, decode := range []struct {
		raw    []byte
		target any
	}{{row.BaseSettings, &value.BaseSettings}, {row.LocalSettings, &value.LocalSettings}, {row.Adoptions, &value.Adoptions}, {row.Effective, &value.Effective}} {
		if err := json.Unmarshal(decode.raw, decode.target); err != nil {
			return ApplicationStandardEnrollment{}, fmt.Errorf("decode standard enrollment: %w", err)
		}
	}
	return value, nil
}

func (s *PgStore) ListApplicationStandardAssignments(ctx context.Context, orgID string) ([]appstandards.Assignment, error) {
	if !validStandardResourceRead(orgID, orgID) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListApplicationStandardAssignments(ctx, s.pool, mustPgUUID(orgID))
	if err != nil {
		return nil, fmt.Errorf("list standard assignments: %w", err)
	}
	result := []appstandards.Assignment{}
	for _, row := range rows {
		result = append(result, appstandards.Assignment{ID: row.ID, OrgID: row.OrgID, Scope: row.Scope, ScopeID: row.ScopeID, StandardID: row.StandardID, AdmissionVersion: row.AdmissionVersion})
	}
	return result, nil
}
