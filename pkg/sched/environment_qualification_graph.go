package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationGraphContextKey struct{}

// WithEnvironmentQualificationGraphRuntimes keeps reviewed dependencies alive
// throughout one bounded visitor, then retires callers before dependencies.
// This internal primitive produces no qualification or activation authority.
func (e *Engine) WithEnvironmentQualificationGraphRuntimes(ctx context.Context, claimed []state.EnvironmentWorkloadQualificationRequest, visit func(context.Context, map[string]state.Instance) error) error {
	store, ok := e.store.(state.EnvironmentQualificationGraphStore)
	qualifier, claimOK := e.store.(state.EnvironmentGitOpsQualificationStore)
	if !ok || !claimOK || visit == nil || len(claimed) == 0 {
		return state.ErrInvalidArgument
	}
	persisted, err := store.EnvironmentQualificationGraphRequests(ctx, claimed[0])
	if err != nil {
		return err
	}
	if len(persisted) != len(claimed) {
		return state.ErrConflict
	}
	ids := make(map[string]bool, len(persisted))
	for _, request := range persisted {
		ids[request.ID] = true
	}
	for _, request := range claimed {
		if request.ExecutionMode == api.ExecutionModeJob || len(request.FrozenInputs.ServiceBindings) != 0 && (e.environmentQualificationServiceURL == nil || request.FrozenInputs.Baseline.EffectiveServiceBindingTransport() == api.ServiceBindingTransportHTTPS) {
			return state.ErrEnvironmentWorkloadPreparationUnavailable
		}
		if !ids[request.ID] {
			return state.ErrConflict
		}
		delete(ids, request.ID)
		if err := e.validateQualificationOwner(ctx, qualifier, request); err != nil {
			return err
		}
		app, err := e.store.AppByID(ctx, request.AppID)
		if err != nil {
			return err
		}
		if request.ExecutionMode != api.ExecutionModeWorker && app.AppProtocol != "" && app.AppProtocol != api.AppProtocolHTTP1 {
			return state.ErrEnvironmentWorkloadPreparationUnavailable
		}
	}
	ordered, err := qualificationGraphExecutionOrder(claimed)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, qualificationGraphContextKey{}, claimed[0].GraphID)
	instances := make(map[string]state.Instance, len(ordered))
	var execute func(context.Context, int) error
	execute = func(ctx context.Context, index int) error {
		if index == len(ordered) {
			return visit(ctx, instances)
		}
		request := ordered[index]
		return e.WithEnvironmentWorkloadQualificationRuntime(ctx, request, func(ctx context.Context, ins state.Instance) error {
			instances[request.Resource] = ins
			return execute(ctx, index+1)
		})
	}
	return execute(ctx, 0)
}

func qualificationGraphExecutionOrder(claimed []state.EnvironmentWorkloadQualificationRequest) ([]state.EnvironmentWorkloadQualificationRequest, error) {
	if len(claimed) == 0 {
		return nil, state.ErrInvalidArgument
	}
	byResource := make(map[string]state.EnvironmentWorkloadQualificationRequest, len(claimed))
	apps := make(map[string]bool, len(claimed))
	for _, request := range claimed {
		if request.GraphID != claimed[0].GraphID || request.Resource == "" || byResource[request.Resource].ID != "" || apps[request.AppID] {
			return nil, state.ErrConflict
		}
		byResource[request.Resource], apps[request.AppID] = request, true
	}
	var result []state.EnvironmentWorkloadQualificationRequest
	visiting, visited := map[string]bool{}, map[string]bool{}
	var walk func(string) error
	walk = func(resource string) error {
		if visiting[resource] {
			return fmt.Errorf("private qualification dependency cycle: %w", state.ErrEnvironmentWorkloadPreparationUnavailable)
		}
		if visited[resource] {
			return nil
		}
		request := byResource[resource]
		visiting[resource] = true
		for _, name := range sortedQualificationBindingNames(request) {
			binding := request.FrozenInputs.ServiceBindings[name]
			target := byResource["workload/"+binding.Workload]
			var port int
			if target.ID == "" || target.AppID != binding.TargetAppID || target.ExecutionMode == api.ExecutionModeWorker || target.ExecutionMode == api.ExecutionModeJob ||
				json.Unmarshal(target.FrozenInputs.Runtime["port"], &port) != nil || port < 0 || port > 65535 {
				return state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			if err := walk(target.Resource); err != nil {
				return err
			}
		}
		visiting[resource], visited[resource] = false, true
		result = append(result, request)
		return nil
	}
	resources := make([]string, 0, len(byResource))
	for resource := range byResource {
		resources = append(resources, resource)
	}
	slices.Sort(resources)
	for _, resource := range resources {
		if err := walk(resource); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func sortedQualificationBindingNames(request state.EnvironmentWorkloadQualificationRequest) []string {
	names := make([]string, 0, len(request.FrozenInputs.ServiceBindings))
	for name := range request.FrozenInputs.ServiceBindings {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// WithEnvironmentQualificationServiceProxy supplies a node-local guest listener
// URL at startup. Production deliberately does not wire this execution opt-in.
func (e *Engine) WithEnvironmentQualificationServiceProxy(resolve func(context.Context, string) (string, error)) *Engine {
	if e == nil {
		return e
	}
	e.environmentQualificationServiceURL = resolve
	return e
}

func (e *Engine) prepareQualificationServiceBindings(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest, nodeID string, spec *AppSpec) error {
	if len(claimed.FrozenInputs.ServiceBindings) == 0 {
		return nil
	}
	if e.environmentQualificationServiceURL == nil || ctx.Value(qualificationGraphContextKey{}) != claimed.GraphID {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	raw, err := e.environmentQualificationServiceURL(ctx, nodeID)
	if err != nil {
		return err
	}
	base, err := url.Parse(raw)
	if err != nil || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Path != "" || base.Opaque != "" {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	ip, err := netip.ParseAddr(base.Hostname())
	// HTTPS requires the existing verified *.internal listener and scoped DNS
	// delivery. A literal bridge IP cannot borrow that certificate authority.
	if err != nil || !ip.Is4() || !ip.IsPrivate() || base.Scheme != "http" || base.Port() != fmt.Sprint(api.ServiceBindingPort) ||
		claimed.FrozenInputs.Baseline.EffectiveServiceBindingTransport() == api.ServiceBindingTransportHTTPS {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	keys := map[string]bool{}
	for _, entry := range spec.APIEnv {
		keys[entry.Key] = true
	}
	for _, entry := range spec.SealedEnv {
		keys[entry.Key] = true
	}
	for _, name := range sortedQualificationBindingNames(claimed) {
		binding := claimed.FrozenInputs.ServiceBindings[name]
		if binding.EnvKey == "" || keys[binding.EnvKey] {
			return state.ErrConflict
		}
		keys[binding.EnvKey] = true
		base.Path = api.EnvironmentQualificationServicePrefix + claimed.GraphID + "/" + name
		spec.APIEnv = append(spec.APIEnv, fcvm.APIEnvEntry{Key: binding.EnvKey, Value: strings.TrimSuffix(base.String(), "/")})
	}
	return nil
}
