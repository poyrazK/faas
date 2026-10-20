package managedpostgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	pipelineSQL "github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ConnectionBudgetStore = (*PostgresStore)(nil)

func (s *PostgresStore) ReadConnectionBudget(ctx context.Context, accountID, databaseID string) (ConnectionBudgetSnapshot, error) {
	row, err := sqlc.New().ReadConnectionBudgetSnapshot(ctx, s.pool, sqlc.ReadConnectionBudgetSnapshotParams{
		AccountID: accountID, DatabaseID: databaseID, PageLimit: api.DataAPIConnectionBudgetMaxBindings,
		HeartbeatSeconds: int32(state.DefaultHeartbeatStaleness.Seconds()),
	})
	if err != nil {
		return ConnectionBudgetSnapshot{}, mapPostgresError(err)
	}
	out := ConnectionBudgetSnapshot{DatabaseID: row.DatabaseID, DatabaseName: row.Name, DatabaseState: row.State,
		BackendFingerprint: row.BackendFingerprint, Generation: row.ObservedGeneration,
		CheckedAt: row.CheckedAt.Time, Truncated: row.Truncated}
	if err := json.Unmarshal(row.Bindings, &out.Bindings); err != nil {
		return out, fmt.Errorf("managedpostgres: decode connection inventory: %w", err)
	}
	if err := json.Unmarshal(row.Capacity, &out.Capacity); err != nil {
		return out, fmt.Errorf("managedpostgres: decode capacity evidence: %w", err)
	}
	out.NativeReplication, err = pipelineSQL.New().PipelineTargetInUse(ctx, s.pool, pipelineSQL.PipelineTargetInUseParams{AccountID: accountID, DatabaseID: databaseID})
	if err != nil {
		return out, mapPostgresError(err)
	}
	t, admission, capacityErr := connectioncapacity.NewStore(s.pool).Target(ctx, accountID, databaseID)
	if capacityErr == nil && t.Generation == row.ObservedGeneration && t.BackendFingerprint == row.BackendFingerprint && row.DataResourceID.Valid && t.DataResourceID == row.DataResourceID.String {
		out.Admission = &admission
	}
	return out, nil
}

func (s *PostgresStore) AppConnectionBudgetDatabaseIDs(ctx context.Context, accountID, appID string) ([]string, error) {
	return sqlc.New().AppConnectionBudgetDatabaseIDs(ctx, s.pool, sqlc.AppConnectionBudgetDatabaseIDsParams{
		AccountID: accountID, AppID: appID, PageLimit: api.DataAPIConnectionBudgetMaxDatabases + 1,
	})
}
