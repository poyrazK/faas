package bindings

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func CaptureManagedServiceProbe(ctx context.Context, store any, app state.App, dep state.Deployment, command []string) (*state.BindingVerificationPin, error) {
	problem := api.NewProblem(http.StatusConflict, api.CodeConflict, "Managed service verification unavailable", "Select an active request/service or worker caller with a resolved frozen HTTPS binding to a request/service target; refresh the inventory before retrying.")
	probeStore, ok := store.(state.ManagedServiceProbeStore)
	if !ok || !api.IsBindingVerificationCommand(command, false) || dep.AppID != app.ID {
		return nil, problem
	}
	spec, err := state.ResolveManagedServiceProbe(ctx, probeStore, app.ID, dep.ID, command[1])
	if err != nil || len(command) == 3 && command[2] != spec.TargetDeploymentID {
		return nil, problem
	}
	return &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, Revision: spec.ContextRevision, TargetDeploymentID: spec.TargetDeploymentID, ManagedService: &spec}, nil
}
