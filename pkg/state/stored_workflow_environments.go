package state

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Stage stored workflows (ADR-20261104120000 amendment 2026-11-22): a
// workflow belongs to production (EnvironmentID "") or to one stage of its
// app's project. Production's readers (ListStoredWorkflows, the runnable
// definitions, schedules, events and webhooks) never see a stage's; a stage's
// workflow is reached only through its own environment and stays disabled
// until a later release runs it on the stage's deployment. Names stay unique
// per app until the contract release.

func (m *MemStore) storedWorkflowStageRecordsLocked(appID, environmentID string) []StoredWorkflow {
	records := make([]StoredWorkflow, 0)
	for _, record := range m.storedWorkflows {
		if record.AppID == appID && record.EnvironmentID == environmentID {
			records = append(records, copyStoredWorkflow(record))
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records
}

// storedWorkflowEnvironmentValidLocked mirrors the PG guard trigger: a stage
// is one of the app's project environments.
func (m *MemStore) storedWorkflowEnvironmentValidLocked(app App, environmentID string) bool {
	if environmentID == "" {
		return true
	}
	env, ok := m.projectEnvironments[environmentID]
	return ok && app.ProjectID != "" && env.ProjectID == app.ProjectID && env.AccountID == app.AccountID
}

func (m *MemStore) ListStageStoredWorkflows(_ context.Context, appID, environmentID string) ([]StoredWorkflow, error) {
	if environmentID == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storedWorkflowStageRecordsLocked(appID, environmentID), nil
}

func (m *MemStore) StoredWorkflowEnvironment(_ context.Context, appID, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.storedWorkflows[appID+"/"+name]
	if !ok {
		return "", ErrNotFound
	}
	return record.EnvironmentID, nil
}

func (s *PgStore) ListStageStoredWorkflows(ctx context.Context, appID, environmentID string) ([]StoredWorkflow, error) {
	if environmentID == "" {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListStageStoredWorkflows(ctx, s.pool, sqlc.ListStageStoredWorkflowsParams{AppID: mustPgUUID(appID), EnvironmentID: mustPgUUID(environmentID)})
	if err != nil {
		return nil, err
	}
	records := make([]StoredWorkflow, 0, len(rows))
	for _, row := range rows {
		records = append(records, storedWorkflowFromSQL(row))
	}
	return records, nil
}

func (s *PgStore) StoredWorkflowEnvironment(ctx context.Context, appID, name string) (string, error) {
	return storedWorkflowEnvironmentTx(ctx, s.pool, sqlc.New(), appID, name)
}

func storedWorkflowEnvironmentTx(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, appID, name string) (string, error) {
	id, err := q.StoredWorkflowEnvironmentByName(ctx, db, sqlc.StoredWorkflowEnvironmentByNameParams{AppID: mustPgUUID(appID), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return pgUUIDStringOrEmpty(id), nil
}

// storedWorkflowEnvironmentConflict refuses a mutation whose name another
// environment of the app holds.
func storedWorkflowEnvironmentConflict(held string, found bool, mutation StoredWorkflowMutation) error {
	if found && held != mutation.EnvironmentID {
		return ErrStoredWorkflowNameInOtherEnvironment
	}
	return nil
}
