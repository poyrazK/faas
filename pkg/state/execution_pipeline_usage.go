package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RunUsageRecord is the timing projection of a terminal execution's ledger row.
type RunUsageRecord struct {
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt time.Time
	WallTimeMS int64
}

type PipelineRunUsageStore interface {
	PipelineRunUsage(context.Context, string, []string) (map[string]RunUsageRecord, error)
}

func (s *PgStore) PipelineRunUsage(ctx context.Context, accountID string, runIDs []string) (map[string]RunUsageRecord, error) {
	if len(runIDs) > api.ExecutionWorkflowManagedMaxSteps*api.ExecutionPipelineMaxAttempts {
		return nil, ErrInvalidArgument
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	ids := make([]pgtype.UUID, 0, len(runIDs))
	for _, raw := range runIDs {
		id, err := parsePgUUID(raw)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		ids = append(ids, id)
	}
	rows, err := sqlc.New().PipelineRunUsage(ctx, s.pool, sqlc.PipelineRunUsageParams{AccountID: account, RunIds: ids})
	if err != nil {
		return nil, mapErr(err)
	}
	records := make(map[string]RunUsageRecord, len(rows))
	for _, row := range rows {
		records[pgUUIDString(row.ExecutionID)] = RunUsageRecord{CreatedAt: row.CreatedAt.Time,
			StartedAt: timestamptzToTimePtr(row.StartedAt), FinishedAt: row.FinishedAt.Time, WallTimeMS: row.WallTimeMs}
	}
	return records, nil
}

func (m *MemStore) PipelineRunUsage(_ context.Context, accountID string, runIDs []string) (map[string]RunUsageRecord, error) {
	if len(runIDs) > api.ExecutionWorkflowManagedMaxSteps*api.ExecutionPipelineMaxAttempts {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	records := make(map[string]RunUsageRecord, len(runIDs))
	for _, id := range runIDs {
		row, ok := m.executionUsageLedger[id]
		if ok && row.AccountID == accountID {
			records[id] = RunUsageRecord{CreatedAt: row.CreatedAt, StartedAt: cloneTimePtr(row.StartedAt),
				FinishedAt: row.FinishedAt, WallTimeMS: row.WallTimeMS}
		}
	}
	return records, nil
}
