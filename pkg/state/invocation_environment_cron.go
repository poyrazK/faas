package state

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// stageCronInvocationShape is the only cron invocation a stage admits
// (ADR-20261104120000): an HTTP request of an environment cron, with no
// queue, destinations, work policy or revision/release pin. Its owner proof
// is the cron row, whose environment never changes.
func stageCronInvocationShape(inv Invocation) bool {
	if inv.Source != InvocationCron || inv.CronID == nil || *inv.CronID == "" || inv.QueueName != "" || inv.QueueBindingID != "" || inv.AckURL != "" ||
		inv.OnSuccessDestinationID != "" || inv.OnFailureDestinationID != "" || inv.WorkPolicyName != "" || inv.WorkPolicyRevision != 0 ||
		len(inv.WorkKeyDigest) != 0 || len(inv.WorkFairnessDigest) != 0 || inv.WorkFairnessLimit != 0 || inv.WorkSequence != 0 || inv.WorkExpiresAt != nil {
		return false
	}
	if len(inv.Headers) == 0 {
		return true
	}
	headers := map[string]string{}
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return false
	}
	revision, release, err := invocationPinHeaders(headers)
	return err == nil && revision == "" && release == ""
}

// invocationCronScopeReader reads the app and environment scope of a cron;
// the stores and their dispatch snapshots implement it.
type invocationCronScopeReader interface {
	CronEnvironmentScope(ctx context.Context, cronID string) (appID, scope string, err error)
}

const cronEnvironmentScopeSQL = `select app_id::text, deployment_scope from crons where id = $1::uuid`

func (s *PgStore) CronEnvironmentScope(ctx context.Context, cronID string) (string, string, error) {
	if _, err := uuid.Parse(cronID); err != nil {
		return "", "", ErrNotFound
	}
	var appID, scope string
	err := s.pool.QueryRow(ctx, cronEnvironmentScopeSQL, cronID).Scan(&appID, &scope)
	return appID, scope, mapErr(err)
}

func (s invocationVersionPolicyReader) CronEnvironmentScope(ctx context.Context, cronID string) (string, string, error) {
	if _, err := uuid.Parse(cronID); err != nil {
		return "", "", ErrNotFound
	}
	var appID, scope string
	err := s.tx.QueryRow(ctx, cronEnvironmentScopeSQL, cronID).Scan(&appID, &scope)
	return appID, scope, mapErr(err)
}

func (m *MemStore) CronEnvironmentScope(_ context.Context, cronID string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cron, ok := m.crons[cronID]
	if !ok {
		return "", "", ErrNotFound
	}
	return cron.AppID, cron.DeploymentScope, nil
}

// The snapshot already holds the store's lock.
func (s memInvocationVersionReader) CronEnvironmentScope(_ context.Context, cronID string) (string, string, error) {
	cron, ok := s.store.crons[cronID]
	if !ok {
		return "", "", ErrNotFound
	}
	return cron.AppID, cron.DeploymentScope, nil
}

// stageCronEnvironment resolves the environment an environment cron's
// invocation belongs to. ok is false for any invocation that is not a cron's
// or whose cron runs in production; such an invocation must not name an
// environment. A stage cron's invocation must have the stage shape and may
// only name its cron's environment and scope.
func stageCronEnvironment(ctx context.Context, store invocationAppReader, inv Invocation) (ProjectEnvironment, bool, error) {
	if inv.Source != InvocationCron || inv.CronID == nil || *inv.CronID == "" {
		return ProjectEnvironment{}, false, nil
	}
	crons, ok := store.(invocationCronScopeReader)
	if !ok {
		// Without the cron's scope only production work may proceed.
		if inv.EnvironmentID != "" || !ProductionDeploymentScope(inv.DeploymentScope) {
			return ProjectEnvironment{}, false, ErrInvocationEnvironmentWorkIsolation
		}
		return ProjectEnvironment{}, false, nil
	}
	cronAppID, cronScope, err := crons.CronEnvironmentScope(ctx, *inv.CronID)
	if err != nil {
		return ProjectEnvironment{}, false, err
	}
	if cronAppID != inv.AppID {
		return ProjectEnvironment{}, false, ErrInvocationEnvironmentWorkIsolation
	}
	if cronScope == "" {
		return ProjectEnvironment{}, false, nil
	}
	if !stageCronInvocationShape(inv) || (inv.DeploymentScope != "" && inv.DeploymentScope != cronScope) {
		return ProjectEnvironment{}, false, ErrInvocationEnvironmentWorkIsolation
	}
	envs, ok := store.(invocationEnvironmentStore)
	if !ok {
		return ProjectEnvironment{}, false, ErrInvocationEnvironmentWorkIsolation
	}
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return ProjectEnvironment{}, false, err
	}
	if app.ProjectID == "" || app.PreviewOfSlug != "" || app.Status == AppDeleted {
		return ProjectEnvironment{}, false, ErrInvocationEnvironmentWorkIsolation
	}
	env, err := envs.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, cronScope)
	if err != nil {
		return ProjectEnvironment{}, false, err
	}
	if env.AccountID != app.AccountID || env.ProjectID != app.ProjectID || env.Slug != cronScope ||
		(inv.EnvironmentID != "" && inv.EnvironmentID != env.ID) {
		return ProjectEnvironment{}, false, ErrInvocationEnvironmentWorkIsolation
	}
	return env, true, nil
}

// prepareStageCronInvocation stamps an environment cron's invocation with its
// environment, scope and a durable id before it is written.
func prepareStageCronInvocation(ctx context.Context, store invocationAppReader, inv Invocation) (Invocation, ProjectEnvironment, bool, error) {
	env, ok, err := stageCronEnvironment(ctx, store, inv)
	if err != nil || !ok {
		if err == nil && inv.EnvironmentID != "" && inv.Source == InvocationCron {
			err = ErrInvocationEnvironmentWorkIsolation
		}
		return inv, env, false, err
	}
	inv.EnvironmentID, inv.DeploymentScope = env.ID, env.Slug
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	}
	return inv, env, true, nil
}

// validateStageCronOwnerLocked mirrors ValidateInvocationEnvironmentClaim's
// cron branch: the invocation's cron belongs to the app and to the stage the
// invocation names.
func (m *MemStore) validateStageCronOwnerLocked(inv Invocation) error {
	app, appOK := m.apps[inv.AppID]
	env, envOK := m.projectEnvironments[inv.EnvironmentID]
	cron, cronOK := m.crons[*inv.CronID]
	if !appOK || !envOK || !cronOK || app.Status == AppDeleted || inv.AccountID != app.AccountID ||
		env.AccountID != app.AccountID || env.ProjectID != app.ProjectID || !invocationStageScope(env.Slug) ||
		cron.AppID != app.ID || cron.DeploymentScope != env.Slug || inv.DeploymentScope != env.Slug {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

// cronEnvironmentLocked is the project environment an environment cron runs
// in, found from its app's project.
func (m *MemStore) cronEnvironmentLocked(app App, cron Cron) (ProjectEnvironment, error) {
	for _, env := range m.projectEnvironments {
		if env.AccountID == app.AccountID && env.ProjectID == app.ProjectID && env.Slug == cron.DeploymentScope && app.ProjectID != "" {
			return env, nil
		}
	}
	return ProjectEnvironment{}, ErrInvocationEnvironmentWorkIsolation
}
