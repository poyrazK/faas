// adr: 20261122200000

package imaged

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// developerPrimeActivation admits the activation schedd sends when a
// `gregale dev` deployment goes live from its healthy prime instead of a
// snapshot (ADR-20261122200000). Only a developer environment's own request
// or service deployment qualifies, and the proof must name its RUNNING
// prime; the store re-checks that runtime inside the activation write.
func (h *Handler) developerPrimeActivation(ctx context.Context, app state.App, dep state.Deployment, ready deploymentReadyPayload, mode string) (context.Context, error) {
	if mode != api.ExecutionModeRequest && mode != api.ExecutionModeService {
		return ctx, errors.New("imaged: developer prime activation is only for request and service workloads")
	}
	if !state.DeveloperPrimeEligible(app, dep) {
		return ctx, errors.New("imaged: developer prime activation requires a developer environment's own deployment")
	}
	if reader, ok := h.store.(state.EnvironmentBranchDeploymentReader); ok {
		branched, err := reader.EnvironmentBranchDeployment(ctx, dep.ID)
		if err != nil {
			return ctx, err
		}
		if branched {
			return ctx, errors.New("imaged: developer prime activation refuses an environment branch deployment")
		}
	}
	if ready.InstanceID == "" {
		return ctx, errors.New("imaged: developer prime activation missing instance_id")
	}
	ins, err := h.store.InstanceByID(ctx, ready.InstanceID)
	if err != nil {
		return ctx, fmt.Errorf("imaged: load developer prime instance: %w", err)
	}
	wantMode := state.InstanceModeNormal
	if mode == api.ExecutionModeService {
		wantMode = state.InstanceModeService
	}
	if ins.AppID != dep.AppID || ins.DeploymentID != dep.ID || ins.Mode != string(wantMode) || ins.State != string(state.StateRunning) {
		return ctx, errors.New("imaged: developer prime proof does not match a running instance of the deployment")
	}
	return state.WithDeveloperPrimeActivation(ctx, dep, ins)
}
