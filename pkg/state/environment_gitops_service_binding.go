package state

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// EnvironmentGitOpsServiceBindingRoute is an exact route from one active
// GitOps-managed workload to a service workload in the same reviewed graph.
// The deployment and release identities come from the active release set, not
// from the guest's Host header. Scheduled Job callers reach this resolver only
// after the live-instance identity lookup proves their claimed scheduled task.
type EnvironmentGitOpsServiceBindingRoute struct {
	CallerAppID        string
	CallerDeploymentID string
	TargetAppID        string
	TargetDeploymentID string
	ReleaseSetID       string
	AccountID          string
	RequireHTTPS       bool
	CallScope          *api.ServiceCallScope
	Reliability        *api.ServiceReliabilityPolicy
}

// EnvironmentGitOpsServiceBindingStore resolves production service aliases
// from the active reviewed workload graph. Alias discovery is separate from
// request authorization: every HTTP request must resolve the exact caller
// deployment and target deployment again.
type EnvironmentGitOpsServiceBindingStore interface {
	ResolveEnvironmentGitOpsServiceBinding(context.Context, string, string, string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error)
}

func environmentGitOpsServiceBindingPolicy(caller, target EnvironmentWorkloadRuntime, service string) (
	requireHTTPS bool, callScope *api.ServiceCallScope, reliability *api.ServiceReliabilityPolicy, err error) {
	if service == "" || caller.Resource == "" || target.Resource != "workload/"+service || caller.AppID == target.AppID {
		return false, nil, nil, ErrConflict
	}
	callerManifest, err := EffectiveEnvironmentWorkloadManifest(caller)
	if err != nil {
		return false, nil, nil, ErrConflict
	}
	callerMode := callerManifest.ExecutionMode
	if callerMode == "" {
		callerMode = api.ExecutionModeRequest
	}
	switch callerMode {
	case api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeWorker, api.ExecutionModeJob:
		// Job identity is accepted only after the live-instance resolver maps a
		// claimed scheduled task to its exact active GitOps app/deployment. The
		// route below still rechecks the release and frozen binding per request.
	default:
		return false, nil, nil, ErrConflict
	}
	targetManifest, err := EffectiveEnvironmentWorkloadManifest(target)
	if err != nil || targetManifest.ExecutionMode == api.ExecutionModeJob || targetManifest.ExecutionMode == api.ExecutionModeWorker {
		return false, nil, nil, ErrConflict
	}
	bound := false
	for _, binding := range caller.ServiceBindings {
		if binding.Workload != service || binding.TargetAppID != target.AppID {
			continue
		}
		bound = true
		break
	}
	if !bound {
		return false, nil, nil, ErrConflict
	}
	callerName := strings.TrimPrefix(caller.Resource, "workload/")
	if callerName == caller.Resource {
		return false, nil, nil, ErrConflict
	}
	if allowedCallers := targetManifest.AllowedServiceCallers; allowedCallers != nil {
		allowed := false
		for _, name := range *allowedCallers {
			allowed = allowed || strings.EqualFold(strings.TrimSpace(name), callerName)
		}
		if !allowed {
			return false, nil, nil, ErrConflict
		}
	}
	if allowedScopes := targetManifest.AllowedServiceCallScopes; allowedScopes != nil {
		scopes, normalizeErr := api.NormalizeServiceCallerScopes(*allowedScopes)
		if normalizeErr != nil {
			return false, nil, nil, ErrConflict
		}
		scope, ok := scopes[strings.ToLower(callerName)]
		if !ok {
			return false, nil, nil, ErrConflict
		}
		callScope = &scope
	}
	requireHTTPS = callerManifest.EffectiveServiceBindingTransport() == api.ServiceBindingTransportHTTPS
	if policy, ok := callerManifest.ServiceReliability[strings.ToLower(service)]; ok {
		if policy.Validate() != nil {
			return false, nil, nil, ErrConflict
		}
		reliability = &policy
	}
	return requireHTTPS, callScope, reliability, nil
}

// EffectiveEnvironmentWorkloadManifest materializes the reviewed workload
// runtime on top of its captured baseline for policy consumers.
func EffectiveEnvironmentWorkloadManifest(frozen EnvironmentWorkloadRuntime) (AppManifest, error) {
	var zero AppManifest
	raw, err := json.Marshal(frozen.Baseline)
	if err != nil {
		return zero, err
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return zero, err
	}
	for key, value := range runtimeManifestValues(frozen.Baseline) {
		values[key] = value
	}
	for key, value := range frozen.Runtime {
		values[key] = value
	}
	raw, err = json.Marshal(values)
	if err != nil {
		return zero, err
	}
	var manifest AppManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return zero, err
	}
	return manifest, nil
}
