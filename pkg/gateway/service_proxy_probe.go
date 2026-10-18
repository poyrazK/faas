package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// Observe the authorized route without waking or forwarding to the target.
// Managed probes use the graph-selected deployment even without guest selectors.
func (p *ServiceProxy) serveServiceBindingProbe(w http.ResponseWriter, r *http.Request, target ServiceTarget, binding *ServiceProxyEnvironmentBinding) {
	if p.provider == nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service endpoint registry is unavailable")
		return
	}
	deploymentID, releaseID, ok := p.serviceBindingProbeRoute(w, r, target.AppID, binding)
	if !ok {
		return
	}
	endpoints, err := p.endpoints(r.Context(), target.AppID)
	if err != nil {
		p.metrics.IncServiceCall(ServiceCallRegistryUnavailable)
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service endpoint registry is unavailable")
		return
	}
	if deploymentID != "" {
		endpoints = serviceEndpointsForDeployment(endpoints, deploymentID)
	}
	if len(endpoints) == 0 {
		p.metrics.IncServiceCall(ServiceCallNoReplica)
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas; probe did not wake the target")
		return
	}
	if deploymentID != "" {
		w.Header().Set(api.TargetDeploymentHeader, deploymentID)
	}
	if releaseID != "" {
		w.Header().Set(api.ReleaseHeader, releaseID)
	}
	w.Header().Set(api.ServiceBindingProbeStageHeader, "complete")
	w.WriteHeader(http.StatusNoContent)
}

func (p *ServiceProxy) serviceBindingProbeRoute(w http.ResponseWriter, r *http.Request, appID string, binding *ServiceProxyEnvironmentBinding) (deploymentID, releaseID string, ok bool) {
	deploymentID, present, valid := serviceDeploymentOverrideFromRequest(r)
	if present && !valid {
		serviceProxyProblem(w, http.StatusBadRequest, "invalid service probe target deployment")
		return "", "", false
	}
	if binding != nil {
		releaseID, releasePresent, releaseValid := serviceReleaseFromRequest(r)
		if releasePresent && !releaseValid {
			serviceProxyProblem(w, http.StatusBadRequest, "X-Gregale-Release must contain one release ID")
			return "", "", false
		}
		if present && deploymentID != binding.TargetDeploymentID {
			serviceProxyProblem(w, http.StatusConflict, "GitOps service probes only inspect the active reviewed target deployment")
			return "", "", false
		}
		if releasePresent && releaseID != binding.ReleaseSetID {
			serviceProxyProblem(w, http.StatusConflict, "GitOps service bindings only route the active reviewed release")
			return "", "", false
		}
		// The managed resolver already proved the target against the reviewed
		// graph. Guest selectors may assert that route but cannot replace it.
		return binding.TargetDeploymentID, binding.ReleaseSetID, true
	}
	if present {
		if p.validateDeployment == nil {
			serviceProxyProblem(w, http.StatusServiceUnavailable, "service deployment validation is unavailable")
			return "", "", false
		}
		live, err := p.validateDeployment(r.Context(), appID, deploymentID)
		if err != nil || !live {
			serviceProxyProblem(w, http.StatusUnprocessableEntity, "service probe target deployment is unavailable")
			return "", "", false
		}
	}
	return deploymentID, "", true
}
