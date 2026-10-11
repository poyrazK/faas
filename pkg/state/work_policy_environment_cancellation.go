package state

import (
	"context"

	"github.com/google/uuid"
)

// EnvironmentWorkCancellationStore cancels only the immutable environment's
// namespace. It can drain an old policy even after the desired policy is deleted.
type EnvironmentWorkCancellationStore interface {
	CancelPendingEnvironmentKeyedInvocations(context.Context, string, string, string, string, string, string, ...string) (WorkCancellation, error)
}

func workCancellationEnvironment(ctx context.Context, store interface {
	invocationAppReader
	ProjectEnvironmentBySlug(context.Context, string, string, string) (ProjectEnvironment, error)
}, accountID, appID, environment string, expectedEnvironmentIDs ...string) (invocationWorkEnvironment, error) {
	info := invocationWorkEnvironment{}
	if environment == "" || !invocationStageScope(environment) || len(expectedEnvironmentIDs) > 1 {
		return info, ErrInvalidArgument
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		return info, err
	}
	if app.AccountID != accountID || app.ProjectID == "" || app.Status == AppDeleted {
		return info, ErrNotFound
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, accountID, app.ProjectID, environment)
	if err != nil {
		return info, err
	}
	if env.AccountID != app.AccountID || env.ProjectID != app.ProjectID {
		return info, ErrNotFound
	}
	if len(expectedEnvironmentIDs) == 1 && expectedEnvironmentIDs[0] != env.ID {
		return info, ErrConflict
	}
	info.app, info.environment = app, env
	return info, nil
}

func environmentWorkCancellationInputs(info invocationWorkEnvironment, policyName, canonicalKey, cancellationID string) (uuid.UUID, [32]byte, error) {
	id, _, err := validateWorkCancellation(info.app.ID, policyName, canonicalKey, cancellationID)
	if err != nil {
		return id, [32]byte{}, ErrInvalidArgument
	}
	digest, err := invocationWorkDomainDigest(info.environment.ID, "key", canonicalKey)
	return id, digest, err
}

func (s *PgStore) CancelPendingEnvironmentKeyedInvocations(ctx context.Context, accountID, appID, environment, policyName, canonicalKey, cancellationID string, expectedEnvironmentIDs ...string) (WorkCancellation, error) {
	info, err := workCancellationEnvironment(ctx, s, accountID, appID, environment, expectedEnvironmentIDs...)
	if err != nil {
		return WorkCancellation{}, err
	}
	id, digest, err := environmentWorkCancellationInputs(info, policyName, canonicalKey, cancellationID)
	if err != nil {
		return WorkCancellation{}, err
	}
	return s.cancelPendingWorkDigest(ctx, info.app.ID, policyName, id, digest, info)
}

func (m *MemStore) CancelPendingEnvironmentKeyedInvocations(ctx context.Context, accountID, appID, environment, policyName, canonicalKey, cancellationID string, expectedEnvironmentIDs ...string) (WorkCancellation, error) {
	info, err := workCancellationEnvironment(ctx, m, accountID, appID, environment, expectedEnvironmentIDs...)
	if err != nil {
		return WorkCancellation{}, err
	}
	id, digest, err := environmentWorkCancellationInputs(info, policyName, canonicalKey, cancellationID)
	if err != nil {
		return WorkCancellation{}, err
	}
	return m.cancelPendingWorkDigest(ctx, info.app.ID, policyName, id, digest, info)
}
