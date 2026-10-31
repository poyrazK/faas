package imaged

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateEnvironmentBranchActivation admits the no-boot activation schedd
// sends for a deployment an environment branch created from its parent's
// artifact (ADR-20261031193635). Only those deployments, and only request or
// service workloads, may go live without a snapshot; their first request
// cold boots.
func (h *Handler) validateEnvironmentBranchActivation(ctx context.Context, dep state.Deployment, ready deploymentReadyPayload, mode string) error {
	if mode != api.ExecutionModeRequest && mode != api.ExecutionModeService {
		return errors.New("imaged: environment branch activation is only for request and service workloads")
	}
	if ready.InstanceID != "" {
		return errors.New("imaged: environment branch activation must not carry instance_id")
	}
	reader, ok := h.store.(state.EnvironmentBranchDeploymentReader)
	if !ok {
		return errors.New("imaged: environment branch activation is unavailable")
	}
	branched, err := reader.EnvironmentBranchDeployment(ctx, dep.ID)
	if err != nil {
		return err
	}
	if !branched {
		return errors.New("imaged: environment branch activation requires a deployment the branch created")
	}
	return nil
}
