package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) retryOperationDefinitions(ctx context.Context, app state.App, dep state.Deployment) ([]api.OperationDefinitionSpec, error) {
	store, ok := s.store.(state.OperationStore)
	if !ok {
		return nil, nil
	}
	defs, err := store.OperationDefinitionsForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return nil, err
	}
	if len(defs) > 0 && !s.operationDefinitionAdmission(app.AccountID, app.ID, dep.Scope) {
		return nil, api.ErrCapacity("new operation admission is disabled")
	}
	specs := make([]api.OperationDefinitionSpec, 0, len(defs))
	for _, def := range defs {
		specs = append(specs, def.Spec)
	}
	return specs, nil
}

func (s *server) installRetryOperations(ctx context.Context, dep state.Deployment, specs []api.OperationDefinitionSpec) error {
	if len(specs) == 0 {
		return nil
	}
	store, ok := s.store.(state.OperationStore)
	if !ok {
		return api.ErrCapacity("operation storage is unavailable")
	}
	app, err := s.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		_, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: app.AccountID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: spec}})
		if err != nil {
			return err
		}
	}
	return nil
}
