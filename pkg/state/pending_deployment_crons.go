package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PendingDeploymentCronStore exposes an accepted intent for project reapply.
// Callers must compare its revision and before set with a fresh owned snapshot.
type PendingDeploymentCronStore interface {
	PendingDeploymentCrons(context.Context, string, string) (*PendingDeploymentCrons, error)
}
type PendingDeploymentCrons struct {
	Intent     api.DeploymentCronStagingRequest
	Revision   int64
	Deployment Deployment
}

func (m *MemStore) PendingDeploymentCrons(ctx context.Context, accountID, appID string) (*PendingDeploymentCrons, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return nil, ErrNotFound
	}
	a, ok := m.deploymentCronActivations[appID]
	if !ok || a.Status != "prepared" {
		return nil, nil
	}
	d, ok := m.cronActivationDeploymentLocked(appID, a)
	if !ok {
		return nil, nil
	}
	return &PendingDeploymentCrons{Intent: a.Intent, Revision: a.Revision, Deployment: d}, nil
}
func (s *PgStore) PendingDeploymentCrons(ctx context.Context, accountID, appID string) (*PendingDeploymentCrons, error) {
	// Scope ownership before reading the internal intent.
	if _, err := s.DeploymentCronStagingSnapshot(ctx, accountID, appID); err != nil {
		return nil, err
	}
	q := sqlc.New()
	a, err := readCronActivation(ctx, q, s.pool, appID)
	if err != nil || a == nil || a.Status != "prepared" {
		return nil, err
	}
	bound, err := q.ReadCronActivationDeployment(ctx, s.pool, sqlc.ReadCronActivationDeploymentParams{AppID: appID, SessionID: a.Intent.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := s.DeploymentByID(ctx, bound.DeploymentID)
	if err != nil {
		return nil, err
	}
	return &PendingDeploymentCrons{Intent: a.Intent, Revision: a.Revision, Deployment: d}, nil
}
