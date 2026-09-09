package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppDeploymentSummary serves the app-scoped release cockpit. The route
// deliberately includes both the app slug and deployment id: resolving the
// slug first keeps cross-account and mismatched deployment probes on the
// same 404 surface as the other app-scoped deployment reads.
func (s *server) getAppDeploymentSummary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}

	id := r.PathValue("id")
	deployment, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil || deployment.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}

	previous, err := s.previousAppDeployment(r.Context(), app.ID, deployment)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read previous deployment"))
		return
	}

	currentResponse := s.deploymentResponse(deployment, app)
	var previousResponse *api.DeploymentResponse
	if previous != nil {
		projected := s.deploymentResponse(*previous, app)
		previousResponse = &projected
	}

	rollbackTargetID, err := s.rollbackTargetID(r.Context(), app.ID, deployment.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read rollback target"))
		return
	}

	changes := make([]api.DeploymentChange, 0)
	if previousResponse != nil {
		changes = deploymentChanges(*previousResponse, currentResponse)
	}
	writeJSON(w, http.StatusOK, api.DeploymentSummaryResponse{
		Deployment:       currentResponse,
		Previous:         previousResponse,
		Changes:          changes,
		RollbackTargetID: rollbackTargetID,
	})
}

// previousAppDeployment returns the nearest older deployment by the same
// created_at cursor contract used by GET /v1/apps/{slug}/deployments.
func (s *server) previousAppDeployment(ctx context.Context, appID string, current state.Deployment) (*state.Deployment, error) {
	var rows []state.Deployment
	var err error
	if current.CreatedAt.IsZero() {
		// Zero timestamps only occur in narrow in-memory fixtures. Read two
		// rows so the current row can be skipped without returning itself.
		rows, err = s.store.ListDeploymentsForApp(ctx, appID, 2, 0)
	} else {
		rows, err = s.listDeploymentsForAppBefore(ctx, appID, current.CreatedAt, 1)
	}
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID != current.ID {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// rollbackTargetID reports the target the existing app rollback operation
// would select today. Only superseded rows are eligible; a missing target is
// a normal first-deploy state and is represented by an omitted field.
func (s *server) rollbackTargetID(ctx context.Context, appID, currentID string) (string, error) {
	target, err := s.store.LatestSupersededDeployment(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if target.ID == currentID {
		return "", nil
	}
	return target.ID, nil
}

// deploymentChanges compares only non-secret release metadata. The field
// names are intentionally stable so dashboards can render a compact diff
// without knowing the full DeploymentResponse implementation.
func deploymentChanges(before, after api.DeploymentResponse) []api.DeploymentChange {
	candidates := []struct {
		field         string
		before, after any
	}{
		{field: "status", before: before.Status, after: after.Status},
		{field: "kind", before: before.Kind, after: after.Kind},
		{field: "build_id", before: before.BuildID, after: after.BuildID},
		{field: "image_digest", before: before.ImageDigest, after: after.ImageDigest},
		{field: "source_url", before: before.SourceURL, after: after.SourceURL},
		{field: "commit_sha", before: before.CommitSHA, after: after.CommitSHA},
		{field: "source_root", before: before.SourceRoot, after: after.SourceRoot},
		{field: "scope", before: before.Scope, after: after.Scope},
		{field: "build_plan", before: before.BuildPlan, after: after.BuildPlan},
		{field: "min_instances", before: before.MinInstances, after: after.MinInstances},
		{field: "traffic_percent", before: before.TrafficPercent, after: after.TrafficPercent},
		{field: "has_overrides", before: before.HasOverrides, after: after.HasOverrides},
		{field: "canary_preset", before: before.CanaryPreset, after: after.CanaryPreset},
		{field: "rollback_on_5xx", before: before.RollbackOn5xx, after: after.RollbackOn5xx},
		{field: "rollout_state", before: before.RolloutState, after: after.RolloutState},
	}
	changes := make([]api.DeploymentChange, 0, len(candidates))
	for _, candidate := range candidates {
		if reflect.DeepEqual(candidate.before, candidate.after) {
			continue
		}
		changes = append(changes, api.DeploymentChange{
			Field:  candidate.field,
			Before: candidate.before,
			After:  candidate.after,
		})
	}
	return changes
}
