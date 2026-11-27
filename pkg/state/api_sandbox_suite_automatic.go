package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxSuiteAutoEnqueuer interface {
	EnqueueAPISandboxSuiteAutomatic(context.Context, string, time.Time) error
}

type APISandboxSuiteAutomaticRequest struct {
	DeploymentID string
	RunID        string
	ClaimUntil   time.Time
}

type APISandboxSuiteAutomaticStore interface {
	ClaimAPISandboxSuiteAutomatic(context.Context, time.Time, time.Duration) (APISandboxSuiteAutomaticRequest, error)
	CheckpointAPISandboxSuiteAutomatic(context.Context, APISandboxSuiteAutomaticRequest, string, *time.Time) error
	GetAPISandboxSuiteAutomaticStatus(context.Context, string) (string, error)
}

func (s *PgStore) EnqueueAPISandboxSuiteAutomatic(ctx context.Context, id string, now time.Time) error {
	return mapErr(sqlc.New().EnqueueAPISandboxSuiteAutomatic(ctx, s.pool, sqlc.EnqueueAPISandboxSuiteAutomaticParams{
		DeploymentID: mustPgUUID(id), RunID: mustPgUUID(uuid.NewString()), NowAt: pgTimestamptzUTC(now),
	}))
}

func (s *PgStore) ClaimAPISandboxSuiteAutomatic(ctx context.Context, now time.Time, lease time.Duration) (APISandboxSuiteAutomaticRequest, error) {
	row, err := sqlc.New().ClaimAPISandboxSuiteAutomatic(ctx, s.pool, sqlc.ClaimAPISandboxSuiteAutomaticParams{
		NowAt: pgTimestamptzUTC(now), ClaimUntil: pgTimestamptzUTC(now.Add(lease)),
	})
	return APISandboxSuiteAutomaticRequest{
		DeploymentID: pgUUIDString(row.DeploymentID), RunID: pgUUIDString(row.RunID), ClaimUntil: row.NextAttemptAt.Time.UTC(),
	}, mapErr(err)
}

func (s *PgStore) CheckpointAPISandboxSuiteAutomatic(ctx context.Context, req APISandboxSuiteAutomaticRequest, status string, next *time.Time) error {
	var due pgtype.Timestamptz
	if next != nil {
		due = pgTimestamptzUTC(*next)
	}
	n, err := sqlc.New().CheckpointAPISandboxSuiteAutomatic(ctx, s.pool, sqlc.CheckpointAPISandboxSuiteAutomaticParams{
		DeploymentID: mustPgUUID(req.DeploymentID), ClaimUntil: pgTimestamptzUTC(req.ClaimUntil),
		Status: status, NextAttemptAt: due,
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) GetAPISandboxSuiteAutomaticStatus(ctx context.Context, id string) (string, error) {
	status, err := sqlc.New().GetAPISandboxSuiteAutomaticStatus(ctx, s.pool, mustPgUUID(id))
	return status, mapErr(err)
}

type apiSandboxSuiteQueuedRequest struct {
	deploymentID string
	runID        string
	status       string
	next         *time.Time
}

func (m *MemStore) EnqueueAPISandboxSuiteAutomatic(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok || d.Status != DeploySnapshotting || len(m.apiSandboxSuites[d.AppID].ScenarioIDs) == 0 ||
		(!m.apiSandboxSuites[d.AppID].RunBeforeApproval && !m.releaseApprovalPolicies[d.AppID].RequiresSandboxSuite()) {
		return nil
	}
	req := m.apiSandboxSuiteAutomatic[id]
	if req.runID != "" && req.status != "disabled" {
		return nil
	}
	if req.runID == "" {
		req.runID = uuid.NewString()
	}
	if m.apiSandboxSuiteAutomatic == nil {
		m.apiSandboxSuiteAutomatic = make(map[string]apiSandboxSuiteQueuedRequest)
	}
	req.deploymentID, req.status, req.next = id, "queued", &now
	m.apiSandboxSuiteAutomatic[id] = req
	return nil
}

func (m *MemStore) ClaimAPISandboxSuiteAutomatic(_ context.Context, now time.Time, lease time.Duration) (APISandboxSuiteAutomaticRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected apiSandboxSuiteQueuedRequest
	for _, req := range m.apiSandboxSuiteAutomatic {
		if req.next != nil && !req.next.After(now) &&
			(selected.next == nil || sandboxComparisonBefore(*req.next, req.deploymentID, *selected.next, selected.deploymentID)) {
			selected = req
		}
	}
	if selected.next == nil {
		return APISandboxSuiteAutomaticRequest{}, ErrNotFound
	}
	claimed := now.Add(lease)
	selected.next = &claimed
	m.apiSandboxSuiteAutomatic[selected.deploymentID] = selected
	return APISandboxSuiteAutomaticRequest{DeploymentID: selected.deploymentID, RunID: selected.runID, ClaimUntil: claimed}, nil
}

func (m *MemStore) CheckpointAPISandboxSuiteAutomatic(_ context.Context, req APISandboxSuiteAutomaticRequest, status string, next *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxSuiteAutomatic[req.DeploymentID]
	if current.next == nil || !current.next.Equal(req.ClaimUntil) {
		return ErrConflict
	}
	current.status, current.next = status, next
	m.apiSandboxSuiteAutomatic[req.DeploymentID] = current
	return nil
}

func (m *MemStore) GetAPISandboxSuiteAutomaticStatus(_ context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.apiSandboxSuiteAutomatic[id]
	if !ok {
		return "", ErrNotFound
	}
	return req.status, nil
}
