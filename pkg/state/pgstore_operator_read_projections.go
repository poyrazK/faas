package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperatorTenantApps(ctx context.Context, accountID string) ([]api.ObsTenantApp, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return nil, fmt.Errorf("operator tenant id: %w", err)
	}
	rows, err := sqlc.New().OperatorTenantApps(ctx, s.pool, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("operator tenant apps: %w", err)
	}
	out := make([]api.ObsTenantApp, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ObsTenantApp{ID: row.ID, Slug: row.Slug, Status: row.Status, Deployments: int(row.DeploymentsLive)})
	}
	return out, nil
}

func (s *PgStore) OperatorCapacityProfiles(ctx context.Context) ([]api.ObsCapacityProfile, error) {
	// Send only the fixed catalog, never workload rows. This keeps SQL and
	// in-memory classification aligned when the shared named profiles change.
	type definition struct {
		Name          string `json:"name"`
		MemoryMB      int    `json:"memory_mb"`
		CPUMillicores int    `json:"cpu_millicores"`
		SortOrder     int    `json:"sort_order"`
	}
	catalog := make([]definition, 0, len(api.ResourceProfiles))
	for i, profile := range api.ResourceProfiles {
		catalog = append(catalog, definition{string(profile.Name), profile.MemoryMB, profile.CPUMillicores, i})
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return nil, fmt.Errorf("operator profile catalog: %w", err)
	}
	rows, err := sqlc.New().OperatorCapacityProfiles(ctx, s.pool, encoded)
	if err != nil {
		return nil, fmt.Errorf("operator capacity profiles: %w", err)
	}
	out := make([]api.ObsCapacityProfile, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ObsCapacityProfile{
			ResourceProfile: row.ResourceProfile, MemoryMB: int(row.MemoryMb), CPUMillicores: int(row.CpuMillicores),
			Apps: row.Apps, LiveInstances: row.LiveInstances, ReservedMemoryMB: row.ReservedMemoryMb, ReservedCPUMillicores: row.ReservedCpuMillicores,
		})
	}
	return out, nil
}
