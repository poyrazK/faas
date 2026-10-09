// adr: 856
package main

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
)

// Both registry and object-backed bindings are immutable. This preflight is
// not a fleet acknowledgement or a SQL/bucket transaction fence.
func (s *server) durableEntityValidatorReleaseProblem(ctx context.Context, app state.App, deployment state.Deployment) *api.Problem {
	if !s.durableEntityValidatorReleaseGateEnabled || !s.durableEntityApps[app.ID] {
		return nil
	}
	bundle, err := s.resolveDurableEntityValidatorBundle(ctx, app.ID, deployment.ID)
	if !s.durableEntityRestoreIsolationEnabled || !s.executionAPIEnabled || err != nil || bundle.AppID != app.ID || bundle.DeploymentID != deployment.ID || deployment.AppID != app.ID || validatorbundle.Validate(bundle) != nil {
		return api.NewProblem(http.StatusConflict, "durable_entity_validator_release_required", "Validator bundle required", "publish the reviewed validator bundle for every deployment receiving increased traffic before retrying")
	}
	return nil
}
