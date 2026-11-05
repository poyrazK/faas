// adr: 570
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ServicePolicySnapshot contains one verified customer-intent view. Endpoint
// health and security generations remain authoritative runtime decisions.
type ServicePolicySnapshot struct {
	InputRevision      string
	AliasAllowed       bool
	Found              bool
	Target             ServiceTarget
	Caller             ServiceCaller
	AuthorizationError error `json:"-"`
	Routing            *ServiceRoutingSnapshot
}

type ServicePolicyPinner func(context.Context, string, string, bool) (ServicePolicySnapshot, error)

type pinnedServicePolicyKey struct{}
type pinnedServicePolicy struct {
	caller, service string
	snapshot        ServicePolicySnapshot
}

func (p *ServiceProxy) pinServicePolicy(w http.ResponseWriter, r *http.Request, caller, service string, alias bool, binding *ServiceProxyEnvironmentBinding) bool {
	if p.policy == nil {
		return false
	}
	defer measureTrafficPhase(r.Context(), trafficPolicy)()
	ctx, cancel := context.WithTimeout(r.Context(), api.TrafficServicePolicyReadTimeout)
	var snapshot ServicePolicySnapshot
	var err error
	if binding != nil && binding.FrozenPolicy {
		snapshot, err = p.frozenServicePolicy(ctx, caller, *binding)
	} else {
		snapshot, err = p.policy(ctx, caller, service, alias)
	}
	if err == nil {
		err = ctx.Err()
	}
	cancel()
	if err == nil {
		snapshot, err = freezeServicePolicy(snapshot, caller)
	}
	if err == nil {
		selectServiceSnapshotDeployment(r.Context(), &snapshot)
	}
	if err != nil {
		recordTrafficRefusal(r.Context(), "policy_unavailable")
		if handleForwardRequestCancellation(w, r, true) {
			return true
		}
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Cache-Control", "no-store")
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service policy snapshot is unavailable")
		return true
	}
	encoded, err := json.Marshal(struct {
		Caller, Service string
		Alias           bool
		Policy          ServicePolicySnapshot
		Retry           RetryPolicy
	}{caller, service, alias, snapshot, p.retryPolicy})
	if err != nil {
		recordTrafficRefusal(r.Context(), "policy_unavailable")
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service policy snapshot is unavailable")
		return true
	}
	revision := policyDigest(encoded)
	pinned := pinnedServicePolicy{caller: caller, service: service, snapshot: snapshot}
	requestCtx := context.WithValue(r.Context(), pinnedServicePolicyKey{}, pinned)
	*r = *r.WithContext(context.WithValue(requestCtx, effectiveTrafficPolicyKey{}, revision))
	return false
}

// The exact graph was verified before policy pinning. Bind its immutable
// selection and policy to the current tenant check; ordinary app discovery
// cannot replace a scoped declaration or select another deployment.
func (p *ServiceProxy) frozenServicePolicy(ctx context.Context, caller string, binding ServiceProxyEnvironmentBinding) (ServicePolicySnapshot, error) {
	if p.authorizeBound == nil {
		return ServicePolicySnapshot{}, ErrServiceProxyUnavailable
	}
	identity, err := p.authorizeBound(ctx, caller, binding.Target.AppID)
	if err == nil && (identity.AppID != caller || identity.AccountID != binding.AccountID) {
		err = ErrServiceProxyDenied
	}
	identity.RequireHTTPS, identity.CallScope, identity.Reliability = binding.RequireHTTPS, binding.CallScope, binding.Reliability
	input, marshalErr := json.Marshal(struct {
		Caller  string
		Binding ServiceProxyEnvironmentBinding
		Allowed bool
	}{caller, binding, err == nil})
	if marshalErr != nil {
		return ServicePolicySnapshot{}, marshalErr
	}
	inputs, _ := ServicePolicyRoutingInputsFromContext(ctx)
	return ServicePolicySnapshot{
		InputRevision: "frozen-service-v1:" + policyDigest(input), AliasAllowed: true, Found: true,
		Target: binding.Target, Caller: identity, AuthorizationError: err,
		Routing: &ServiceRoutingSnapshot{
			CallerDeploymentID: inputs.CallerDeploymentID, ReleaseResolved: true,
			ReleaseID: binding.ReleaseSetID, ReleaseDeploymentID: binding.TargetDeploymentID,
			RequestedReleaseID: inputs.RequestedReleaseID, ReleaseVerdict: "allowed",
			SelectedDeploymentID: binding.TargetDeploymentID,
		},
	}, nil
}

func freezeServicePolicy(snapshot ServicePolicySnapshot, caller string) (ServicePolicySnapshot, error) {
	if snapshot.InputRevision == "" || (snapshot.Found && snapshot.Target.AppID == "") ||
		(snapshot.Found && snapshot.AuthorizationError == nil && (snapshot.Caller.AppID != caller || snapshot.Caller.AccountID == "")) {
		return ServicePolicySnapshot{}, errors.New("inconsistent service policy snapshot")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ServicePolicySnapshot{}, err
	}
	var frozen ServicePolicySnapshot
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return ServicePolicySnapshot{}, err
	}
	frozen.AuthorizationError = snapshot.AuthorizationError
	if frozen.Routing != nil {
		frozen.Routing.ReleaseError = snapshot.Routing.ReleaseError
	}
	return frozen, nil
}

func (p *ServiceProxy) serviceAliasAllowed(ctx context.Context, caller, service string) (bool, error) {
	if pinned, ok := ctx.Value(pinnedServicePolicyKey{}).(pinnedServicePolicy); ok && pinned.caller == caller && pinned.service == service {
		return pinned.snapshot.AliasAllowed, nil
	}
	if p.policy != nil {
		return false, ErrServiceProxyUnavailable
	}
	return p.allowAlias(ctx, caller, service)
}

func (p *ServiceProxy) authorizeServiceCaller(ctx context.Context, caller, target string) (ServiceCaller, error) {
	if pinned, ok := ctx.Value(pinnedServicePolicyKey{}).(pinnedServicePolicy); ok && pinned.caller == caller && pinned.snapshot.Target.AppID == target {
		return pinned.snapshot.Caller, pinned.snapshot.AuthorizationError
	}
	if p.policy != nil {
		return ServiceCaller{}, ErrServiceProxyUnavailable
	}
	return p.authorize(ctx, caller, target)
}

func isTrafficPolicyProof(name string) bool {
	name = strings.TrimSpace(name)
	if len(name) >= len(http.TrailerPrefix) && strings.EqualFold(name[:len(http.TrailerPrefix)], http.TrailerPrefix) {
		name = strings.TrimSpace(name[len(http.TrailerPrefix):])
	}
	return strings.EqualFold(name, TrafficPolicyRevisionHeader)
}

func stripTrafficPolicyProof(header http.Header) {
	for name := range header {
		if isTrafficPolicyProof(name) {
			header.Del(name)
		}
	}
}
