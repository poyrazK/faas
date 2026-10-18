package apidserver

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Selectors choose candidate evidence. They cannot grant permission or make
// an unpublished release routable.
func (s *server) selectBindingServiceTargets(r *http.Request, app state.App, caller state.Deployment, targets map[string]string) *api.Problem {
	if caller.EnvironmentWorkloadManaged() && len(targets) != 0 {
		return api.ErrValidation("managed service targets are selected by the active reviewed environment release")
	}
	if len(targets) > api.ProjectReleaseSetMaxMembers {
		return api.ErrValidation("too many service target selectors")
	}
	declared := map[string]bool{}
	for _, binding := range app.Manifest.ServiceBindings {
		declared[binding.Service] = true
	}
	for service, id := range targets {
		if !declared[service] || !api.IsServiceBindingTargetProbeCommand([]string{api.AppTaskServiceBindingTargetProbeCommand, service, id}, false) {
			return api.ErrValidation("service targets require declared services and canonical deployment UUIDs")
		}
		target, err := s.store.AppBySlug(r.Context(), service)
		if err != nil || target.AccountID != app.AccountID || target.Status == state.AppDeleted || target.PreviewOfSlug != "" {
			return api.ErrValidation("service target is unavailable")
		}
		deployment, problem := s.selectBindingVerificationDeployment(r.Context(), target, id)
		if problem != nil || deployment.EnvironmentWorkloadHeld() || deployment.Scope != caller.Scope {
			return api.ErrValidation("service target must be an eligible live deployment in the caller scope")
		}
	}
	return nil
}

func bindingServiceTargets(r *http.Request) map[string]string {
	var targets map[string]string
	_ = json.Unmarshal([]byte(r.URL.Query().Get("service_targets")), &targets)
	return targets
}

func (s *server) validateBindingServiceTargets(r *http.Request, app state.App) *api.Problem {
	raw := r.URL.Query().Get("service_targets")
	if raw == "" {
		return nil
	}
	var targets map[string]string
	if json.Unmarshal([]byte(raw), &targets) != nil || targets == nil || r.URL.Query().Get("deployment_id") == "" {
		return api.ErrValidation("service_targets requires a JSON object and an exact caller deployment")
	}
	caller, problem := s.selectBindingVerificationDeployment(r.Context(), app, r.URL.Query().Get("deployment_id"))
	if problem != nil {
		return problem
	}
	return s.selectBindingServiceTargets(r, app, caller, targets)
}
