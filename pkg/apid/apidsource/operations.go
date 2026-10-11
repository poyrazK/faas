package apidsource

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func installSourceOperations(ctx context.Context, store Store, dep state.Deployment, specs []api.OperationDefinitionSpec) error {
	if len(specs) == 0 {
		return nil
	}
	operations, ok := store.(state.OperationStore)
	if !ok {
		return fmt.Errorf("operation storage is unavailable")
	}
	reader, ok := store.(interface {
		AppByID(context.Context, string) (state.App, error)
	})
	if !ok {
		return fmt.Errorf("operation owner resolution is unavailable")
	}
	app, err := reader.AppByID(ctx, dep.AppID)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		_, err := operations.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: app.AccountID,
			OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: spec}})
		if err != nil {
			return err
		}
	}
	return nil
}
