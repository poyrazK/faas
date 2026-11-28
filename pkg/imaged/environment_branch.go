package imaged

import (
	"context"
	"errors"
	"fmt"

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
	dataReady, err := reader.EnvironmentBranchDataReady(ctx, dep.ID)
	if err != nil {
		return err
	}
	if !dataReady {
		return errors.New("imaged: environment branch activation waits for the branch's data")
	}
	return nil
}

// holdForEnvironmentBranchData reports whether dep waits for the data of the
// environment branch it was built into. Stores without branches hold nothing.
func (h *Handler) holdForEnvironmentBranchData(ctx context.Context, dep state.Deployment) (bool, error) {
	reader, ok := h.store.(state.EnvironmentBranchDeploymentReader)
	if !ok {
		return false, nil
	}
	ready, err := reader.EnvironmentBranchDataReady(ctx, dep.ID)
	if err != nil {
		return true, fmt.Errorf("imaged: read environment branch data: %w", err)
	}
	if !ready {
		h.log.Debug("imaged: deployment waits for its environment branch's data", "deployment_id", dep.ID, "app_id", dep.AppID)
	}
	return !ready, nil
}
