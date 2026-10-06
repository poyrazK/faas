package state

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The network identity is observed by the node's guest listener. Graph and
// binding names are selectors, never caller credentials or fallback aliases.
type EnvironmentQualificationServiceRequest struct {
	NodeID, HostIP, GraphID, Binding string
}

type EnvironmentQualificationServiceRoute struct {
	Caller, Target EnvironmentQualificationExecution
	Port           int
	RequireHTTPS   bool
	CallScope      *api.ServiceCallScope
	Deadline       time.Time
}

type EnvironmentQualificationServiceStore interface {
	EnvironmentQualificationNetworkCaller(context.Context, string, string) (bool, error)
	ResolveEnvironmentQualificationService(context.Context, EnvironmentQualificationServiceRequest) (EnvironmentQualificationServiceRoute, error)
}

func qualificationNetworkIdentityValid(nodeID, hostIP string) bool {
	ip, err := netip.ParseAddr(hostIP)
	return qualificationRecoveryUUIDValid(nodeID) && err == nil && ip.Is4() && ip.String() == hostIP
}

func qualificationServiceRequestValid(request EnvironmentQualificationServiceRequest) bool {
	return qualificationNetworkIdentityValid(request.NodeID, request.HostIP) && qualificationRecoveryUUIDValid(request.GraphID) && api.ValidAppSlug(request.Binding)
}

func qualificationServiceExecutionCurrent(request EnvironmentWorkloadQualificationRequest, status EnvironmentQualificationExecutionStatus, ins Instance, now time.Time) bool {
	expected := qualificationExecution(request, ins, status.Execution.CleanupToken)
	if status.CaptureInstanceID != "" {
		expected = qualificationRestoreExecution(request, ins, status.Execution.CleanupToken, request.ReservedInstanceID)
	}
	return qualificationLeaseMatches(request, request, now) && status.DispatchStarted && status.RetiredAt == nil &&
		status.Execution == expected &&
		ins.State == string(StateRunning) && ins.Netns != "" && ins.HostIP != "" && ins.WakeID != ""
}

func qualificationServiceRoute(caller, target EnvironmentWorkloadQualificationRequest, callerStatus, targetStatus EnvironmentQualificationExecutionStatus, bindingName string) (EnvironmentQualificationServiceRoute, error) {
	var zero EnvironmentQualificationServiceRoute
	binding, declared := caller.FrozenInputs.ServiceBindings[bindingName]
	if !declared || caller.GraphID != target.GraphID || caller.AppID == target.AppID || binding.TargetAppID != target.AppID ||
		target.Resource != "workload/"+binding.Workload || target.ExecutionMode == api.ExecutionModeWorker || target.ExecutionMode == api.ExecutionModeJob ||
		caller.FrozenInputs.SourceID != target.FrozenInputs.SourceID || caller.FrozenInputs.EnvironmentID != target.FrozenInputs.EnvironmentID ||
		caller.FrozenInputs.Scope != target.FrozenInputs.Scope || caller.FrozenInputs.DefinitionDigest != target.FrozenInputs.DefinitionDigest ||
		caller.FrozenInputs.RevisionID != target.FrozenInputs.RevisionID || caller.FrozenInputs.Generation != target.FrozenInputs.Generation ||
		caller.FrozenInputs.IntentVersion != target.FrozenInputs.IntentVersion || caller.FrozenInputs.PlanHash != target.FrozenInputs.PlanHash {
		return zero, ErrConflict
	}
	callerName := strings.TrimPrefix(caller.Resource, "workload/")
	policy := target.FrozenInputs.Baseline
	if policy.AllowedServiceCallers != nil {
		allowed := false
		for _, name := range *policy.AllowedServiceCallers {
			allowed = allowed || strings.EqualFold(name, callerName)
		}
		if !allowed {
			return zero, ErrConflict
		}
	}
	route := EnvironmentQualificationServiceRoute{Caller: callerStatus.Execution, Target: targetStatus.Execution,
		RequireHTTPS: caller.FrozenInputs.Baseline.EffectiveServiceBindingTransport() == api.ServiceBindingTransportHTTPS,
		Deadline:     *caller.LeaseUntil}
	if target.LeaseUntil.Before(route.Deadline) {
		route.Deadline = *target.LeaseUntil
	}
	if policy.AllowedServiceCallScopes != nil {
		scopes, err := api.NormalizeServiceCallerScopes(*policy.AllowedServiceCallScopes)
		scope, allowed := scopes[callerName]
		if err != nil || !allowed {
			return zero, ErrConflict
		}
		route.CallScope = &scope
	}
	// Until the native boot receipt pins inferred protocol/port inputs, private
	// dependency routing requires an explicit reviewed port. Never guess from
	// mutable app metadata or route to a sibling's inferred endpoint.
	port, explicit := target.FrozenInputs.Runtime["port"]
	if !explicit || json.Unmarshal(port, &route.Port) != nil {
		return zero, ErrEnvironmentWorkloadPreparationUnavailable
	}
	if route.Port == 0 {
		route.Port = api.DefaultAppPort
	}
	if route.Port < 1 || route.Port > 65535 {
		return zero, ErrConflict
	}
	// Routing authority is not a cleanup capability. Neither guest nor gateway
	// consumers receive the scheduler's private retirement token.
	route.Caller.CleanupToken, route.Target.CleanupToken = "", ""
	return route, nil
}
