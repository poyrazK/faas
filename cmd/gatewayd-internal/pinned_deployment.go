package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// deploymentReader is the slice of the production store the alias
// not-serving refusal reads (H8-7).
type deploymentReader interface {
	DeploymentByID(context.Context, string) (state.Deployment, error)
}

// The production store must answer pinned-deployment lookups; without these
// the gateway silently fell back to "App concurrency reached" for an alias
// whose deployment had been superseded (production hunt #8).
var (
	_ deploymentReader               = (*state.PgStore)(nil)
	_ gateway.PinnedDeploymentLookup = weightsStoreAdapter{}
)

func (a weightsStoreAdapter) PinnedDeployment(ctx context.Context, deploymentID string) (gateway.PinnedDeployment, error) {
	reader, ok := a.store.(deploymentReader)
	if !ok {
		return gateway.PinnedDeployment{}, errors.New("gatewayd: deployment store cannot read deployments")
	}
	dep, err := reader.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return gateway.PinnedDeployment{}, err
	}
	return gateway.PinnedDeployment{
		ID:       dep.ID,
		Revision: dep.Revision,
		Status:   string(dep.Status),
		Live:     dep.Status == state.DeployLive,
	}, nil
}
