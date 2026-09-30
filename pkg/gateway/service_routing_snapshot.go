// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// These inputs are constructed after node source identity is verified. The
// selection key is local entropy, never a guest claim or policy fingerprint.
type ServicePolicyRoutingInputs struct {
	CallerDeploymentID, RequestedReleaseID, OverrideDeploymentID, VersionKey, SelectionKey string
	ResolveRelease, ReleasePresent, ReleaseValid, OverridePresent, OverrideValid           bool
	Probe, Secure                                                                          bool
	Method, TargetPath                                                                     string
}

type servicePolicyRoutingInputsKey struct{}

func WithServicePolicyRoutingInputs(ctx context.Context, inputs ServicePolicyRoutingInputs) context.Context {
	return context.WithValue(ctx, servicePolicyRoutingInputsKey{}, inputs)
}

func ServicePolicyRoutingInputsFromContext(ctx context.Context) (ServicePolicyRoutingInputs, bool) {
	inputs, ok := ctx.Value(servicePolicyRoutingInputsKey{}).(ServicePolicyRoutingInputs)
	return inputs, ok
}

type ServiceRoutingSnapshot struct {
	CallerDeploymentID                 string
	ReleaseResolved                    bool
	ReleaseID, ReleaseDeploymentID     string
	RequestedReleaseID, ReleaseVerdict string
	ReleaseError                       error `json:"-"`
	OverrideDeploymentID               string
	OverrideChecked, OverrideAllowed   bool
	Weights                            []DeploymentWeightsRow
	SelectedDeploymentID               string `json:"-"`
}

func (p *ServiceProxy) withServiceRoutingInputs(r *http.Request, sourceDeployment, targetPath string, probe bool) {
	if p.policy == nil {
		return
	}
	release, releasePresent, releaseValid := serviceReleaseFromRequest(r)
	override, overridePresent, overrideValid := serviceDeploymentOverrideFromRequest(r)
	key, _ := versionAffinityKeyFromRequest(r)
	inputs := ServicePolicyRoutingInputs{CallerDeploymentID: sourceDeployment, TargetPath: targetPath, Method: r.Method,
		RequestedReleaseID: release, ReleasePresent: releasePresent, ReleaseValid: releaseValid,
		OverrideDeploymentID: override, OverridePresent: overridePresent, OverrideValid: overrideValid,
		ResolveRelease: p.resolveRelease != nil && p.resolveCallerIdentity != nil,
		VersionKey:     key, SelectionKey: uuid.NewString(), Probe: probe, Secure: r.TLS != nil}
	*r = *r.WithContext(WithServicePolicyRoutingInputs(r.Context(), inputs))
}

// Select once from the pinned weights before wake. Exact/release pins take
// precedence; retries and late endpoint refresh never cross this deployment.
func selectServiceSnapshotDeployment(ctx context.Context, snapshot *ServicePolicySnapshot) {
	inputs, ok := ServicePolicyRoutingInputsFromContext(ctx)
	if !ok || snapshot.Routing == nil {
		return
	}
	routing := snapshot.Routing
	if inputs.Probe || !inputs.ReleaseValid || routing.ReleaseError != nil {
		return
	}
	if routing.ReleaseDeploymentID != "" {
		routing.SelectedDeploymentID = routing.ReleaseDeploymentID
		return
	}
	if inputs.OverridePresent {
		if inputs.OverrideValid && routing.OverrideChecked && routing.OverrideAllowed {
			routing.SelectedDeploymentID = inputs.OverrideDeploymentID
		}
		return
	}
	key := inputs.VersionKey
	if key == "" {
		key = inputs.SelectionKey
	}
	routing.SelectedDeploymentID, _ = AffinityDeploymentFromWeights(snapshot.Target.AppID, key, routing.Weights)
}

func (p *ServiceProxy) resolveServiceRelease(ctx context.Context, caller, source, target, requested string) (string, string, error) {
	if p.policy == nil {
		return p.resolveRelease(ctx, caller, source, target, requested)
	}
	policy, ok := ctx.Value(pinnedServicePolicyKey{}).(pinnedServicePolicy)
	if !ok || policy.caller != caller || policy.snapshot.Target.AppID != target || policy.snapshot.Routing == nil {
		return "", "", ErrServiceProxyUnavailable
	}
	routing := policy.snapshot.Routing
	if !routing.ReleaseResolved || routing.CallerDeploymentID != source || routing.RequestedReleaseID != requested {
		return "", "", ErrServiceProxyUnavailable
	}
	return routing.ReleaseID, routing.ReleaseDeploymentID, routing.ReleaseError
}

func (p *ServiceProxy) validateServiceDeployment(ctx context.Context, app, deployment string) (bool, error) {
	if p.policy == nil {
		return p.validateDeployment(ctx, app, deployment)
	}
	policy, ok := ctx.Value(pinnedServicePolicyKey{}).(pinnedServicePolicy)
	if !ok || policy.snapshot.Target.AppID != app || policy.snapshot.Routing == nil {
		return false, ErrServiceProxyUnavailable
	}
	routing := policy.snapshot.Routing
	if !routing.OverrideChecked || routing.OverrideDeploymentID != deployment {
		return false, ErrServiceProxyUnavailable
	}
	return routing.OverrideAllowed, nil
}

func (p *ServiceProxy) serviceSnapshotDeployment(ctx context.Context, app string) (string, error) {
	policy, ok := ctx.Value(pinnedServicePolicyKey{}).(pinnedServicePolicy)
	if !ok || policy.snapshot.Target.AppID != app || policy.snapshot.Routing == nil {
		return "", ErrServiceProxyUnavailable
	}
	if policy.snapshot.Routing.SelectedDeploymentID == "" {
		return "", errors.New("service has no deployment in the admitted routing policy")
	}
	return policy.snapshot.Routing.SelectedDeploymentID, nil
}
