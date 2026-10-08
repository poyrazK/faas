package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

// MemStore mirrors PgStore's app_forks semantics: the same validation, the
// same live-deployment requirement, limits counted over unexpired active
// rows, and the same cancellation rules.

func (m *MemStore) CreateAppFork(_ context.Context, params CreateAppForkParams) (AppFork, error) {
	p, err := validateCreateAppFork(params)
	if err != nil {
		return AppFork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appForks == nil {
		m.appForks = make(map[string]AppFork)
	}

	appActive, accountActive := 0, 0
	for _, fork := range m.appForks {
		if fork.AccountID != p.AccountID || !fork.Status.Active() || !fork.ExpiresAt.After(p.CreatedAt) {
			continue
		}
		accountActive++
		if fork.AppID == p.AppID {
			appActive++
		}
	}
	if err := appForkLimitExceeded(p, appActive, accountActive); err != nil {
		return AppFork{}, err
	}

	app, ok := m.apps[p.AppID]
	if !ok || app.AccountID != p.AccountID || app.Status == AppDeleted {
		return AppFork{}, ErrAppForkDeploymentUnavailable
	}
	deployment, ok := m.deployments[p.DeploymentID]
	if !ok || deployment.AppID != p.AppID || deployment.Status != DeployLive {
		return AppFork{}, ErrAppForkDeploymentUnavailable
	}

	fork := AppFork{
		ID: uuid.NewString(), AccountID: p.AccountID, AppID: p.AppID, DeploymentID: p.DeploymentID,
		RequestedBy: p.RequestedBy, Status: AppForkQueued, TTLSeconds: p.TTLSeconds,
		ExpiresAt: p.CreatedAt.Add(time.Duration(p.TTLSeconds) * time.Second),
		CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt,
	}
	m.appForks[fork.ID] = fork
	return fork, nil
}

func (m *MemStore) AppForkByID(_ context.Context, accountID, appID, forkID string) (AppFork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fork, ok := m.appForks[forkID]
	if !ok || fork.AccountID != accountID || fork.AppID != appID {
		return AppFork{}, ErrNotFound
	}
	return fork, nil
}

func (m *MemStore) ListAppForks(_ context.Context, accountID, appID string, limit int) ([]AppFork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppFork, 0)
	for _, fork := range m.appForks {
		if fork.AccountID == accountID && fork.AppID == appID {
			out = append(out, fork)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit = clampAppForkListLimit(limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) RequestAppForkCancellation(_ context.Context, accountID, appID, forkID string, requestedAt time.Time) (AppFork, error) {
	if requestedAt.IsZero() {
		return AppFork{}, ErrAppForkInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fork, ok := m.appForks[forkID]
	if !ok || fork.AccountID != accountID || fork.AppID != appID {
		return AppFork{}, ErrNotFound
	}
	if fork.Status.Terminal() {
		return fork, nil
	}
	at := requestedAt.UTC().Truncate(time.Microsecond)
	if fork.CancelRequested == nil {
		fork.CancelRequested = &at
	}
	if fork.Status == AppForkQueued {
		fork.Status = AppForkCancelled
		fork.FinishedAt = &at
	}
	if at.After(fork.UpdatedAt) {
		fork.UpdatedAt = at
	}
	m.appForks[forkID] = fork
	return fork, nil
}
