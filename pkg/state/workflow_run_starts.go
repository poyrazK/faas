package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WorkflowRunStartStore records which app started a run with a function's
// start() (ADR-20261113120824 §3); only that app reads the run or sends it
// events from its VM. It is optional so the Store interface does not grow
// for one gateway route.
type WorkflowRunStartStore interface {
	// RecordWorkflowRunStart is idempotent: a retried start keeps the first
	// record.
	RecordWorkflowRunStart(ctx context.Context, runID, starterAppID string) error
	// WorkflowRunStarter returns the starting app, or ErrNotFound for a run
	// no function started.
	WorkflowRunStarter(ctx context.Context, runID string) (string, error)
}

// RecordWorkflowRunStart records the run's starting app.
func (s *PgStore) RecordWorkflowRunStart(ctx context.Context, runID, starterAppID string) error {
	run, err := operationUUID(runID)
	if err != nil {
		return err
	}
	app, err := operationUUID(starterAppID)
	if err != nil {
		return err
	}
	if err := sqlc.New().InsertWorkflowRunStart(ctx, s.pool, sqlc.InsertWorkflowRunStartParams{RunID: run, StarterAppID: app}); err != nil {
		return fmt.Errorf("state: record workflow run start: %w", mapErr(err))
	}
	return nil
}

// WorkflowRunStarter returns the run's starting app.
func (s *PgStore) WorkflowRunStarter(ctx context.Context, runID string) (string, error) {
	run, err := operationUUID(runID)
	if err != nil {
		return "", ErrNotFound
	}
	starter, err := sqlc.New().GetWorkflowRunStarter(ctx, s.pool, run)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: workflow run starter: %w", err)
	}
	return starter, nil
}

// RecordWorkflowRunStart mirrors PgStore.RecordWorkflowRunStart.
func (m *MemStore) RecordWorkflowRunStart(_ context.Context, runID, starterAppID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workflowRuns[runID]; !ok {
		return ErrNotFound
	}
	if m.workflowRunStarts == nil {
		m.workflowRunStarts = map[string]string{}
	}
	if _, ok := m.workflowRunStarts[runID]; !ok {
		m.workflowRunStarts[runID] = starterAppID
	}
	return nil
}

// WorkflowRunStarter mirrors PgStore.WorkflowRunStarter.
func (m *MemStore) WorkflowRunStarter(_ context.Context, runID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	starter, ok := m.workflowRunStarts[runID]
	if !ok {
		return "", ErrNotFound
	}
	return starter, nil
}

// WorkflowRunStartLookup reads the account-app start binding for a retained run.
type WorkflowRunStartLookup interface {
	LookupWorkflowRunIDByStartKey(ctx context.Context, appID, workflowName, key string) (string, error)
}

// LookupWorkflowRunIDByStartKey returns only the ID in the non-tenant binding.
func (s *PgStore) LookupWorkflowRunIDByStartKey(ctx context.Context, appID, workflowName, key string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text
		FROM workflow_runs
		WHERE app_id=$1 AND workflow_name=$2 AND platform_tenant_id IS NULL AND create_idempotency_key=$3
	`, appID, workflowName, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrWorkflowRunNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: look up workflow start key: %w", err)
	}
	return id, nil
}

// LookupWorkflowRunIDByStartKey mirrors the PostgreSQL non-tenant lookup.
func (m *MemStore) LookupWorkflowRunIDByStartKey(_ context.Context, appID, workflowName, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.workflowRunCreateKeys[workflowRunCreateKey{appID: appID, workflowName: workflowName, key: key}]
	if !ok {
		return "", ErrWorkflowRunNotFound
	}
	if _, ok = m.workflowRuns[entry.runID]; !ok {
		return "", ErrWorkflowRunNotFound
	}
	return entry.runID, nil
}
