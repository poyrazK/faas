package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Run before queue insertion or keyed lane mutation. A delivery-time rejection
// alone would be too late: keep_latest could already supersede production work.
func validateInvocationWorkEnvironment(ctx context.Context, store invocationPinScopeStore, inv Invocation, keyed bool) error {
	if !keyed && !invocationHasSharedWorkProducer(inv) && inv.WorkPolicyName == "" && len(inv.WorkKeyDigest) == 0 &&
		inv.OnSuccessDestinationID == "" && inv.OnFailureDestinationID == "" {
		return nil
	}
	headers := map[string]string{}
	if len(inv.Headers) > 0 && json.Unmarshal(inv.Headers, &headers) != nil {
		return ErrInvalidArgument
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil || (revision == "" && release == "") {
		return err
	}
	scope, err := store.ResolveInvocationPinScope(ctx, inv.AppID, revision, release)
	if err != nil {
		return err
	}
	if invocationStageScope(scope) {
		if reader, ok := store.(invocationAppReader); ok {
			if bound, err := validateBoundQueueEnvironment(ctx, reader, inv, scope); bound || err != nil {
				return err
			}
		}
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

// ResolveInvocationPinScope reads identity only. ResolveInvocationVersion still
// checks retention, live deployment status and membership before delivery.
func (s *PgStore) ResolveInvocationPinScope(ctx context.Context, appID, revision, release string) (string, error) {
	appUUID, revisionUUID, releaseUUID, err := invocationPinScopeIDs(appID, revision, release)
	if err != nil {
		return "", err
	}
	scope, err := sqlc.New().ReadInvocationPinScope(ctx, s.pool, sqlc.ReadInvocationPinScopeParams{
		AppID: appUUID, RevisionID: revisionUUID, ReleaseID: releaseUUID,
	})
	if err != nil {
		return "", fmt.Errorf("state: invocation pin scope: %w", mapErr(err))
	}
	return scope, nil
}

func invocationPinScopeIDs(appID, revision, release string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	var appUUID, revisionUUID, releaseUUID pgtype.UUID
	if appUUID.Scan(appID) != nil || !appUUID.Valid || (revision == "") == (release == "") {
		return appUUID, revisionUUID, releaseUUID, ErrInvalidArgument
	}
	for _, value := range []string{revision, release} {
		if value != "" {
			parsed, err := uuid.Parse(value)
			if err != nil || parsed == uuid.Nil {
				return appUUID, revisionUUID, releaseUUID, ErrInvalidArgument
			}
		}
	}
	if revision != "" {
		_ = revisionUUID.Scan(revision)
	} else {
		_ = releaseUUID.Scan(release)
	}
	return appUUID, revisionUUID, releaseUUID, nil
}

func (m *MemStore) ResolveInvocationPinScope(_ context.Context, appID, revision, release string) (string, error) {
	if _, _, _, err := invocationPinScopeIDs(appID, revision, release); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return "", ErrNotFound
	}
	if revision != "" {
		dep, ok := m.deployments[revision]
		if !ok || dep.AppID != app.ID {
			return "", ErrNotFound
		}
		return normalizedDeploymentScope(dep.Scope), nil
	}
	set, ok := m.projectReleaseSets[release]
	if !ok || app.ProjectID == "" || set.ProjectID != app.ProjectID || set.AccountID != app.AccountID {
		return "", ErrNotFound
	}
	return set.EnvironmentSlug, nil
}
