package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type InvocationEnvironmentOwnerReader interface {
	InvocationEnvironmentID(context.Context, string) (string, error)
}

func resolveInvocationEnvironmentAdmission(ctx context.Context, store interface {
	invocationAppReader
	invocationPinScopeStore
	invocationEnvironmentStore
}, inv Invocation) (Invocation, invocationWorkEnvironment, error) {
	info := invocationWorkEnvironment{}
	if bound, err := validateBoundQueueEnvironment(ctx, store, inv, inv.DeploymentScope); bound || err != nil {
		return inv, info, err
	}
	var headers map[string]string
	if len(inv.Headers) > 0 && json.Unmarshal(inv.Headers, &headers) != nil {
		return inv, info, ErrInvalidArgument
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return inv, info, err
	}
	if revision == "" && release == "" {
		if inv.EnvironmentID != "" {
			return inv, info, ErrInvalidArgument
		}
		return inv, info, nil
	}
	scope, err := store.ResolveInvocationPinScope(ctx, inv.AppID, revision, release)
	if err != nil {
		return inv, info, err
	}
	if !invocationStageScope(scope) {
		if inv.EnvironmentID != "" {
			return inv, info, ErrInvalidArgument
		}
		return inv, info, nil
	}
	if !stageKeyedInvocationSupported(inv) {
		return inv, info, ErrInvocationEnvironmentWorkIsolation
	}
	base := inv
	base.ID, base.EnvironmentID = "", ""
	prepared, version, err := ResolveInvocationVersion(ctx, store, base)
	if err != nil {
		return inv, info, err
	}
	info.app, err = store.AppByID(ctx, inv.AppID)
	if err != nil {
		return inv, info, err
	}
	info.environment, err = store.ProjectEnvironmentBySlug(ctx, info.app.AccountID, info.app.ProjectID, version.Scope)
	if err != nil {
		return inv, info, err
	}
	if inv.EnvironmentID != "" && inv.EnvironmentID != info.environment.ID {
		return inv, info, ErrConflict
	}
	info.deployment = version.DeploymentID
	inv.Headers, inv.EnvironmentID = prepared.Headers, info.environment.ID
	inv.DeploymentScope = prepared.DeploymentScope
	if inv.AccountID == "" {
		inv.AccountID = info.app.AccountID
	}
	return inv, info, nil
}

func validateInvocationEnvironmentOwner(ctx context.Context, store invocationAppReader, inv Invocation, version InvocationVersion) error {
	ownerID := inv.EnvironmentID
	if reader, ok := store.(InvocationEnvironmentOwnerReader); ok && inv.ID != "" {
		stored, err := reader.InvocationEnvironmentID(ctx, inv.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && stored != ownerID {
			return ErrInvocationEnvironmentWorkIsolation
		}
		if err == nil {
			ownerID = stored
		}
	}
	if ownerID == "" {
		return nil
	}
	if !invocationStageScope(version.Scope) || version.DeploymentID == "" {
		return ErrInvocationEnvironmentWorkIsolation
	}
	reader, ok := store.(invocationEnvironmentStore)
	if !ok {
		return ErrInvocationEnvironmentWorkIsolation
	}
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return err
	}
	env, err := reader.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, version.Scope)
	if err != nil || env.ID != ownerID || env.AccountID != app.AccountID || env.ProjectID != app.ProjectID {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func (s *PgStore) InvocationEnvironmentID(ctx context.Context, id string) (string, error) {
	// A non-UUID identity (workflow-*, synthetic correlation IDs) cannot name
	// a ledger row, so it has no stored owner. MemStore already reports
	// ErrNotFound; returning ErrInvalidArgument here failed every workflow
	// step on production-us rc.242/rc.243 ("synth invoke resolve version").
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return "", ErrNotFound
	}
	row, err := sqlc.New().ReadInvocationEnvironmentOwner(ctx, s.pool, mustPgUUID(id))
	return row.EnvironmentID, mapErr(err)
}

func (m *MemStore) InvocationEnvironmentID(_ context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok {
		return "", ErrNotFound
	}
	return inv.EnvironmentID, nil
}

// All scoped writers and claims take this shared environment lock before a
// lane/row lock. Environment deletion takes the exclusive lock first, so it
// observes a complete admission or wins before any queue mutation.
func lockInvocationEnvironmentDB(ctx context.Context, db sqlc.DBTX, appID, accountID, environmentID string) error {
	if environmentID == "" {
		return nil
	}
	id, err := sqlc.New().LockInvocationEnvironmentAdmission(ctx, db, sqlc.LockInvocationEnvironmentAdmissionParams{
		AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID), EnvironmentID: mustPgUUID(environmentID)})
	if err != nil {
		return mapErr(err)
	}
	if pgUUIDString(id) != environmentID {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func lockInvocationEnvironmentClaimDB(ctx context.Context, db sqlc.DBTX, id string) error {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return ErrInvalidArgument
	}
	owner, err := sqlc.New().ReadInvocationEnvironmentOwner(ctx, db, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	if err := lockInvocationEnvironmentDB(ctx, db, pgUUIDString(owner.AppID), pgUUIDString(owner.AccountID), owner.EnvironmentID); err != nil {
		return err
	}
	if owned, err := validateInvocationQueueClaimDB(ctx, db, id, true); owned || err != nil {
		return err
	}
	bound, err := sqlc.New().ValidateBoundQueueInvocationClaim(ctx, db, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	if bound {
		return nil
	}
	valid, err := sqlc.New().ValidateInvocationEnvironmentClaim(ctx, db, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	if !valid {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func (m *MemStore) validateInvocationEnvironmentClaimLocked(inv Invocation) error {
	if owned, err := m.validateInvocationQueueClaimLocked(inv, true); owned || err != nil {
		return err
	}
	if owned, err := m.validateBoundQueueClaimLocked(inv); owned || err != nil {
		return err
	}
	if inv.EnvironmentID == "" {
		var pins map[string]string
		if json.Unmarshal(inv.Headers, &pins) == nil {
			revision, release, _ := invocationPinHeaders(pins)
			dep := m.deployments[revision]
			set := m.projectReleaseSets[release]
			app := m.apps[inv.AppID]
			if (dep.AppID == inv.AppID && invocationStageScope(dep.Scope)) || (set.ProjectID == app.ProjectID && set.AccountID == app.AccountID && invocationStageScope(set.EnvironmentSlug)) {
				return ErrInvocationEnvironmentWorkIsolation
			}
		}
		return nil
	}
	if !stageKeyedInvocationSupported(inv) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return ErrInvocationEnvironmentWorkIsolation
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return err
	}
	dep, ok := m.deployments[revision]
	if release != "" {
		set, exists := m.projectReleaseSets[release]
		app := m.apps[inv.AppID]
		env := m.projectEnvironments[inv.EnvironmentID]
		if !exists || set.AccountID != app.AccountID || set.ProjectID != app.ProjectID || set.EnvironmentSlug != env.Slug || !releasePubliclyUsable(set, time.Now()) {
			return ErrInvocationEnvironmentWorkIsolation
		}
		dep, ok = m.deployments[releaseMemberForApp(set, inv.AppID)]
	}
	if !ok {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return m.validateInvocationEnvironmentLocked(inv, invocationWorkEnvironment{deployment: dep.ID})
}

func bindInvocationEnvironmentDB(ctx context.Context, db sqlc.DBTX, info invocationWorkEnvironment, inv Invocation) error {
	count, err := sqlc.New().BindInvocationEnvironment(ctx, db, sqlc.BindInvocationEnvironmentParams{
		InvocationID: mustPgUUID(inv.ID), AppID: mustPgUUID(info.app.ID), AccountID: mustPgUUID(info.app.AccountID),
		EnvironmentID: mustPgUUID(info.environment.ID), DeploymentID: mustPgUUID(info.deployment)})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func (m *MemStore) validateInvocationEnvironmentLocked(inv Invocation, info invocationWorkEnvironment) error {
	if inv.EnvironmentID == "" {
		return nil
	}
	app, ok := m.apps[inv.AppID]
	env, found := m.projectEnvironments[inv.EnvironmentID]
	dep, deployed := m.deployments[info.deployment]
	if !ok || !found || !deployed || app.Status == AppDeleted || inv.AccountID != app.AccountID || env.AccountID != app.AccountID || env.ProjectID != app.ProjectID ||
		dep.AppID != app.ID || normalizedDeploymentScope(dep.Scope) != env.Slug || dep.Status != DeployLive {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}
