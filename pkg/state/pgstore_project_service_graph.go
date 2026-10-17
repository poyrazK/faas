package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ResolveProjectServiceBinding(ctx context.Context, callerAppID, callerDeploymentID, service string, requested ...string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	var zero EnvironmentGitOpsServiceBindingRoute
	if callerDeploymentID == "" {
		return zero, false, false, nil
	}
	if _, err := operationUUID(callerAppID); err != nil {
		return zero, false, false, err
	}
	if _, err := operationUUID(callerDeploymentID); err != nil {
		return zero, false, false, err
	}
	requestedID := ""
	if len(requested) > 0 {
		requestedID = requested[0]
	}
	raw, err := sqlc.New().ReadProjectServiceGraph(ctx, s.pool, sqlc.ReadProjectServiceGraphParams{CallerAppID: callerAppID, CallerDeploymentID: callerDeploymentID, RequestedID: requestedID})
	if err != nil {
		return zero, false, false, mapErr(err)
	}
	var input projectServiceGraphInput
	if json.Unmarshal(raw, &input) != nil {
		return zero, true, false, ErrConflict
	}
	return resolveProjectServiceGraph(input, normalizeProjectServiceName(service))
}
