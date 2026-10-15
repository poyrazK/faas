package apidserver

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func loadReviewedSourceManifest(path string, app state.App, plan api.Plan, reviewed bool) (*gregalemanifest.Manifest, *api.Problem) {
	if reviewed {
		return nil, nil
	}
	return loadSourceRefManifest(path, app, plan)
}

// Retain declarations from the exact serving baseline, rather than installing
// definitions supplied by the new repository. Admission fences baseline drift.
func (s *server) reviewedSourceDeclarations(ctx context.Context, acct state.Account, app state.App, review api.AppReleaseReview) ([]api.WorkflowSpec, []api.OperationDefinitionSpec, *api.Problem) {
	if len(review.ServingDeploymentIDs) == 0 {
		return nil, nil, nil
	}
	if len(review.ServingDeploymentIDs) != 1 {
		return nil, nil, api.ErrValidation("reviewed source staging requires a single serving baseline")
	}
	current, err := s.store.DeploymentByID(ctx, review.ServingDeploymentIDs[0])
	if err != nil || current.AppID != app.ID || current.Scope != review.Scope {
		return nil, nil, api.ErrSourceRefStale("serving declaration baseline changed")
	}
	var workflows []api.WorkflowSpec
	if len(current.Workflows) > 0 && json.Unmarshal(current.Workflows, &workflows) != nil {
		return nil, nil, api.ErrCapacity("could not read current workflow declarations")
	}
	store, ok := s.store.(state.OperationStore)
	if !ok {
		return nil, nil, api.ErrCapacity("operation declaration storage is unavailable")
	}
	operations, err := store.OperationDefinitionsForDeployment(ctx, acct.ID, app.ID, current.ID)
	if err != nil {
		return nil, nil, api.ErrCapacity("could not read current operation declarations")
	}
	specs := make([]api.OperationDefinitionSpec, 0, len(operations))
	for _, operation := range operations {
		specs = append(specs, operation.Spec)
	}
	return workflows, specs, nil
}

func reviewedSourceReleaseCommand(path string, app state.App, manifest *gregalemanifest.Manifest, reviewed bool) (deploymentReleaseCommand, *api.Problem) {
	if reviewed {
		return deploymentReleaseCommand{}, nil
	}
	return resolveSourceReleaseCommand(path, app, manifest)
}
