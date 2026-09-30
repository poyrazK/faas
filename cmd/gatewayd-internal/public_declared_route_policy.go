// adr: 375
package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func resolvePublicDeclaredRoutePolicy(ctx context.Context, reader state.PublicHostPolicyReader, app *gateway.App) error {
	if app.PinnedDeploymentScope != "" {
		policy, err := reader.PublicHostRoutePolicy(ctx, app.AccountID, app.ID, app.PinnedDeploymentScope)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err == nil {
			app.OnlyAllowDeclaredRoutes = policy.OnlyAllowDeclaredRoutes
			app.DeclaredRoutes = gatewayDeclaredRoutes(policy.DeclaredRoutes)
		}
	}
	contract := &gateway.ImportedRoutePolicy{}
	if len(app.DeclaredRoutes) == 0 {
		doc, err := reader.PublicHostOpenAPIDoc(ctx, app.ID, app.AccountID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		contract.Document, contract.Found = doc, err == nil
	}
	app.ImportedRoutePolicy = contract
	return nil
}
