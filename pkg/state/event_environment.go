package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventEnvironmentStore resolves the project environment a published event
// belongs to (ADR-20261104120000). Production is the empty environment: every
// method returns "" for it, so an event is stamped only with a stage.
type EventEnvironmentStore interface {
	// AccountEventEnvironment validates an environment ID named by an
	// account-level publish. ErrNotFound when it is not the account's.
	AccountEventEnvironment(ctx context.Context, accountID, environmentID string) (string, error)
	// AppEventEnvironment resolves an environment slug of the app's project.
	// ErrNotFound when the project has no such environment.
	AppEventEnvironment(ctx context.Context, accountID, appID, slug string) (string, error)
	// DeploymentEventEnvironment is the environment of the deployment that
	// published from inside a guest.
	DeploymentEventEnvironment(ctx context.Context, accountID, appID, deploymentID string) (string, error)
}

func (s *PgStore) AccountEventEnvironment(ctx context.Context, accountID, environmentID string) (string, error) {
	account, errA := parsePgUUID(accountID)
	environment, errE := parsePgUUID(environmentID)
	if errA != nil || errE != nil {
		return "", ErrNotFound
	}
	row, err := sqlc.New().EventEnvironmentForAccount(ctx, s.pool, sqlc.EventEnvironmentForAccountParams{AccountID: account, EnvironmentID: environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("event environment: %w", err)
	}
	if IsProductionScope(row.Slug) {
		return "", nil
	}
	return row.ID, nil
}

func (s *PgStore) AppEventEnvironment(ctx context.Context, accountID, appID, slug string) (string, error) {
	if IsProductionScope(slug) {
		return "", nil
	}
	account, errA := parsePgUUID(accountID)
	app, errP := parsePgUUID(appID)
	if errA != nil || errP != nil {
		return "", ErrNotFound
	}
	id, err := sqlc.New().EventEnvironmentForApp(ctx, s.pool, sqlc.EventEnvironmentForAppParams{AccountID: account, AppID: app, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("app event environment: %w", err)
	}
	return id, nil
}

func (s *PgStore) DeploymentEventEnvironment(ctx context.Context, accountID, appID, deploymentID string) (string, error) {
	account, errA := parsePgUUID(accountID)
	app, errP := parsePgUUID(appID)
	deployment, errD := parsePgUUID(deploymentID)
	if errA != nil || errP != nil || errD != nil {
		return "", ErrNotFound
	}
	id, err := sqlc.New().DeploymentEventEnvironment(ctx, s.pool, sqlc.DeploymentEventEnvironmentParams{AccountID: account, AppID: app, DeploymentID: deployment})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("deployment event environment: %w", err)
	}
	return id, nil
}

func (m *MemStore) AccountEventEnvironment(ctx context.Context, accountID, environmentID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	env, ok := m.projectEnvironments[environmentID]
	if !ok || env.AccountID != accountID {
		return "", ErrNotFound
	}
	if IsProductionScope(env.Slug) {
		return "", nil
	}
	return env.ID, nil
}

func (m *MemStore) AppEventEnvironment(ctx context.Context, accountID, appID, slug string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if IsProductionScope(slug) {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.ProjectID == "" {
		return "", ErrNotFound
	}
	if id := m.projectEnvironmentIDLocked(app.ProjectID, slug); id != "" {
		return id, nil
	}
	return "", ErrNotFound
}

func (m *MemStore) DeploymentEventEnvironment(ctx context.Context, accountID, appID, deploymentID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	app, appOK := m.apps[appID]
	if !ok || !appOK || d.AppID != appID || app.AccountID != accountID {
		return "", ErrNotFound
	}
	if IsProductionScope(d.Scope) || app.ProjectID == "" {
		return "", nil
	}
	return m.projectEnvironmentIDLocked(app.ProjectID, d.Scope), nil
}

func (m *MemStore) projectEnvironmentIDLocked(projectID, slug string) string {
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.Slug == slug {
			return env.ID
		}
	}
	return ""
}

// memEventInEnvironment mirrors the replay queries' environment filter: an
// event replays only to consumers of its own environment ("" is production).
func memEventInEnvironment(payload []byte, environmentID string) bool {
	var event struct {
		EnvironmentID string `json:"environmentid"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return false
	}
	return event.EnvironmentID == environmentID ||
		event.EnvironmentID != "" && canonicalMemUUID(event.EnvironmentID) == canonicalMemUUID(environmentID)
}
