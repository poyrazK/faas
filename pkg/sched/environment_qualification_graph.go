package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationGraphContextKey struct{}
type qualificationGraphDispatchNodeContextKey struct{}
type qualificationGraphRequestsContextKey struct{}
type qualificationGraphServiceListenerContextKey struct{}

type qualificationGraphServiceListener struct {
	nodeID string
	bases  map[api.ServiceBindingTransport]string
}

func withRenewedQualificationGraphRequest(ctx context.Context, renewed state.EnvironmentWorkloadQualificationRequest) context.Context {
	requests, ok := ctx.Value(qualificationGraphRequestsContextKey{}).([]state.EnvironmentWorkloadQualificationRequest)
	if !ok || renewed.GraphID == "" {
		return ctx
	}
	updated := slices.Clone(requests)
	for i := range updated {
		if updated[i].ID == renewed.ID && updated[i].GraphID == renewed.GraphID {
			updated[i] = renewed
			return context.WithValue(ctx, qualificationGraphRequestsContextKey{}, updated)
		}
	}
	return ctx
}

func validateQualificationGraphClaimSubset(ctx context.Context, persisted, claimed []state.EnvironmentWorkloadQualificationRequest,
	receipts state.EnvironmentQualificationJobSmokeReceiptStore) error {
	if len(persisted) == 0 || len(claimed) == 0 {
		return state.ErrConflict
	}
	graphID := persisted[0].GraphID
	if graphID == "" {
		return state.ErrConflict
	}
	persistedIDs := make(map[string]state.EnvironmentWorkloadQualificationRequest, len(persisted))
	resources := make(map[string]bool, len(persisted))
	for _, request := range persisted {
		if request.GraphID != graphID || request.ID == "" || request.Resource == "" || persistedIDs[request.ID].ID != "" || resources[request.Resource] {
			return state.ErrConflict
		}
		persistedIDs[request.ID] = request
		resources[request.Resource] = true
	}
	claimedIDs := make(map[string]bool, len(claimed))
	for _, request := range claimed {
		if request.GraphID != graphID || request.ID == "" || claimedIDs[request.ID] || persistedIDs[request.ID].ID == "" {
			return state.ErrConflict
		}
		claimedIDs[request.ID] = true
	}
	for _, request := range persisted {
		if claimedIDs[request.ID] {
			continue
		}
		if request.ExecutionMode != api.ExecutionModeJob || receipts == nil {
			return state.ErrConflict
		}
		receipt, err := receipts.EnvironmentQualificationJobSmokeReceipt(ctx, request.ID, request.Attempt)
		if err != nil || !qualificationJobSmokeReceiptMatchesRequest(receipt, request) {
			return errors.Join(state.ErrConflict, err)
		}
	}
	return nil
}

func qualificationJobSmokeReceiptMatchesRequest(receipt state.EnvironmentQualificationJobSmokeReceipt,
	request state.EnvironmentWorkloadQualificationRequest) bool {
	evidence, err := state.NewEnvironmentQualificationJobSmokeEvidence(request, receipt.InstanceID, 0, "succeeded", 0)
	return err == nil && receipt.RequestID == request.ID && receipt.Attempt == request.Attempt && receipt.GraphID == request.GraphID &&
		receipt.Resource == evidence.Resource && receipt.PolicyID == evidence.PolicyID &&
		receipt.PolicySHA256 == evidence.PolicySHA256 && receipt.ResultSHA256 == evidence.ResultSHA256 &&
		receipt.InstanceID == request.ReservedInstanceID
}

// WithEnvironmentQualificationGraphRuntimes keeps reviewed dependencies alive
// throughout one bounded visitor, then retires callers before dependencies.
// This internal primitive produces no qualification or activation authority.
func (e *Engine) WithEnvironmentQualificationGraphRuntimes(ctx context.Context, claimed []state.EnvironmentWorkloadQualificationRequest, visit func(context.Context, map[string]state.Instance) error) error {
	store, ok := e.store.(state.EnvironmentQualificationGraphStore)
	qualifier, claimOK := e.store.(state.EnvironmentGitOpsQualificationStore)
	_, configReceiptOK := e.store.(state.EnvironmentQualificationConfigReceiptStore)
	if !ok || !claimOK || !configReceiptOK || visit == nil || len(claimed) == 0 {
		return state.ErrInvalidArgument
	}
	persisted, err := store.EnvironmentQualificationGraphRequests(ctx, claimed[0])
	if err != nil {
		return err
	}
	receipts, _ := e.store.(state.EnvironmentQualificationJobSmokeReceiptStore)
	if err := validateQualificationGraphClaimSubset(ctx, persisted, claimed, receipts); err != nil {
		return err
	}
	for _, request := range claimed {
		if len(request.FrozenInputs.ServiceBindings) != 0 && !e.hasEnvironmentQualificationServiceProxy() {
			return state.ErrEnvironmentWorkloadPreparationUnavailable
		}
		if err := e.validateQualificationOwner(ctx, qualifier, request); err != nil {
			return err
		}
		app, err := e.store.AppByID(ctx, request.AppID)
		if err != nil {
			return err
		}
		if app.AppProtocol != "" && app.AppProtocol != api.AppProtocolHTTP1 {
			return state.ErrEnvironmentWorkloadPreparationUnavailable
		}
	}
	ordered, err := qualificationGraphExecutionOrder(claimed)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, qualificationGraphContextKey{}, claimed[0].GraphID)
	requests := slices.Clone(claimed)
	ctx = context.WithValue(ctx, qualificationGraphRequestsContextKey{}, slices.Clone(requests))
	instances := make(map[string]state.Instance, len(ordered))
	var execute func(context.Context, int) error
	execute = func(ctx context.Context, index int) error {
		if index == len(ordered) {
			return visit(ctx, instances)
		}
		request := ordered[index]
		if err := renewQualificationGraphClaims(ctx, qualifier, requests); err != nil {
			return fmt.Errorf("renew qualification graph claims: %w", err)
		}
		requestByResource := make(map[string]state.EnvironmentWorkloadQualificationRequest, len(requests))
		for _, renewed := range requests {
			requestByResource[renewed.Resource] = renewed
		}
		if renewed, exists := requestByResource[request.Resource]; exists {
			request = renewed
		}
		ctx = context.WithValue(ctx, qualificationGraphRequestsContextKey{}, slices.Clone(requests))
		if request.ExecutionMode == api.ExecutionModeJob {
			beforeStart := func(startCtx context.Context, ins state.Instance) error {
				withJob := make(map[string]state.Instance, len(instances)+1)
				for resource, existing := range instances {
					withJob[resource] = existing
				}
				withJob[request.Resource] = ins
				return e.validateQualificationGraphServiceRoutesForCaller(startCtx, withJob, request.Resource)
			}
			if _, err := e.withEnvironmentWorkloadQualificationJob(ctx, request, beforeStart); err != nil {
				return fmt.Errorf("qualify job workload %s: %w", request.Resource, err)
			}
			return execute(ctx, index+1)
		}
		if err := e.WithEnvironmentWorkloadQualificationRuntime(ctx, request, func(ctx context.Context, ins state.Instance) error {
			instances[request.Resource] = ins
			if nodeID, pinned := ctx.Value(qualificationGraphDispatchNodeContextKey{}).(string); !pinned || nodeID == "" {
				ctx = context.WithValue(ctx, qualificationGraphDispatchNodeContextKey{}, ins.NodeID)
			}
			return execute(ctx, index+1)
		}); err != nil {
			return fmt.Errorf("qualify graph workload %s: %w", request.Resource, err)
		}
		return nil
	}
	return execute(ctx, 0)
}

// preflightQualificationGraphServiceListener resolves and validates the
// listener for the node selected by the first graph member. It runs before
// instance admission or VM effects, then carries the same node-bound URL
// through the rest of the graph window.
func (e *Engine) preflightQualificationGraphServiceListener(ctx context.Context, nodeID string) (context.Context, error) {
	graphID, graph := ctx.Value(qualificationGraphContextKey{}).(string)
	if !graph || graphID == "" {
		return ctx, nil
	}
	if listener, ok := ctx.Value(qualificationGraphServiceListenerContextKey{}).(qualificationGraphServiceListener); ok {
		if listener.nodeID != nodeID {
			return ctx, state.ErrConflict
		}
		return ctx, nil
	}
	requests, ok := ctx.Value(qualificationGraphRequestsContextKey{}).([]state.EnvironmentWorkloadQualificationRequest)
	if !ok || len(requests) == 0 {
		return ctx, state.ErrConflict
	}
	needsListener := false
	for _, request := range requests {
		needsListener = needsListener || len(request.FrozenInputs.ServiceBindings) > 0
	}
	if !needsListener {
		return ctx, nil
	}
	if nodeID == "" || !e.hasEnvironmentQualificationServiceProxy() {
		return ctx, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	bases := make(map[api.ServiceBindingTransport]string, 2)
	for _, request := range requests {
		if len(request.FrozenInputs.ServiceBindings) == 0 {
			continue
		}
		transport := request.FrozenInputs.Baseline.EffectiveServiceBindingTransport()
		if _, ok := bases[transport]; ok {
			continue
		}
		raw, err := e.resolveEnvironmentQualificationServiceURL(ctx, nodeID, transport)
		if err != nil {
			return ctx, err
		}
		if _, err := parseQualificationServiceBase(raw, transport); err != nil {
			return ctx, err
		}
		bases[transport] = raw
	}
	listener := qualificationGraphServiceListener{nodeID: nodeID, bases: bases}
	return context.WithValue(ctx, qualificationGraphServiceListenerContextKey{}, listener), nil
}

// validateQualificationGraphServiceRoutes proves that every frozen binding in
// the current graph resolves to the exact live target instance in the same
// execution window. The state store remains authoritative for caller identity,
// lease freshness, runtime receipts and binding policy; this boundary also
// checks the returned route against the graph instances before capture or
// smoke evidence can be recorded.
func (e *Engine) validateQualificationGraphServiceRoutes(ctx context.Context, instances map[string]state.Instance) error {
	return e.validateQualificationGraphServiceRoutesForCaller(ctx, instances, "")
}

func (e *Engine) validateQualificationGraphServiceRoutesForCaller(ctx context.Context, instances map[string]state.Instance, callerResource string) error {
	requests, ok := ctx.Value(qualificationGraphRequestsContextKey{}).([]state.EnvironmentWorkloadQualificationRequest)
	graphID, graphOK := ctx.Value(qualificationGraphContextKey{}).(string)
	nodeID, nodeOK := ctx.Value(qualificationGraphDispatchNodeContextKey{}).(string)
	if !ok || !graphOK || graphID == "" || !nodeOK || nodeID == "" || len(requests) == 0 {
		return fmt.Errorf("qualification service route context is incomplete: %w", state.ErrConflict)
	}
	requestByResource := make(map[string]state.EnvironmentWorkloadQualificationRequest, len(requests))
	for _, request := range requests {
		if request.GraphID != graphID || request.Resource == "" || requestByResource[request.Resource].ID != "" {
			return fmt.Errorf("qualification service route graph request is invalid: %w", state.ErrConflict)
		}
		requestByResource[request.Resource] = request
	}
	serviceStore, serviceOK := e.store.(state.EnvironmentQualificationServiceStore)
	matchedCaller := callerResource == ""
	for _, request := range requests {
		if callerResource != "" && request.Resource != callerResource {
			continue
		}
		matchedCaller = true
		if len(request.FrozenInputs.ServiceBindings) == 0 {
			continue
		}
		if !serviceOK {
			return state.ErrEnvironmentWorkloadPreparationUnavailable
		}
		caller, exists := instances[request.Resource]
		if !exists && request.ExecutionMode == api.ExecutionModeJob {
			receipts, ok := e.store.(state.EnvironmentQualificationJobSmokeReceiptStore)
			if !ok {
				return state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			receipt, err := receipts.EnvironmentQualificationJobSmokeReceipt(ctx, request.ID, request.Attempt)
			if err != nil || !qualificationJobSmokeReceiptMatchesRequest(receipt, request) {
				return errors.Join(state.ErrConflict, err)
			}
			continue
		}
		if !exists || caller.ID == "" || caller.NodeID != nodeID || caller.State != string(state.StateRunning) || caller.HostIP == "" || caller.WakeID == "" {
			return fmt.Errorf("qualification service caller runtime is not published: %w", state.ErrConflict)
		}
		for _, bindingName := range sortedQualificationBindingNames(request) {
			binding := request.FrozenInputs.ServiceBindings[bindingName]
			targetRequest, exists := requestByResource["workload/"+binding.Workload]
			if !exists || targetRequest.AppID != binding.TargetAppID {
				return state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			target, exists := instances[targetRequest.Resource]
			if !exists || target.ID == "" || target.NodeID != nodeID || target.State != string(state.StateRunning) || target.HostIP == "" || target.WakeID == "" {
				return fmt.Errorf("qualification service target runtime is not published: %w", state.ErrConflict)
			}
			route, err := serviceStore.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{
				NodeID: nodeID, HostIP: caller.HostIP, GraphID: graphID, Binding: bindingName,
			})
			if err != nil {
				return err
			}
			wantPort, err := qualificationGraphBindingPort(targetRequest)
			if err != nil {
				return err
			}
			callerMatches := route.Caller.InstanceID == caller.ID && route.Caller.RequestID == request.ID && route.Caller.GraphID == graphID &&
				route.Caller.Attempt == request.Attempt && route.Caller.AppID == request.AppID && route.Caller.DeploymentID == request.DeploymentID &&
				route.Caller.Resource == request.Resource && route.Caller.NodeID == nodeID && route.Caller.WakeID == caller.WakeID && route.Caller.CleanupToken == ""
			targetMatches := route.Target.InstanceID == target.ID && route.Target.RequestID == targetRequest.ID && route.Target.GraphID == graphID &&
				route.Target.Attempt == targetRequest.Attempt && route.Target.AppID == targetRequest.AppID && route.Target.DeploymentID == targetRequest.DeploymentID &&
				route.Target.Resource == targetRequest.Resource && route.Target.NodeID == nodeID && route.Target.WakeID == target.WakeID && route.Target.CleanupToken == ""
			policyMatches := route.Port == wantPort && route.RequireHTTPS == (request.FrozenInputs.Baseline.EffectiveServiceBindingTransport() == api.ServiceBindingTransportHTTPS)
			deadlineSet := !route.Deadline.IsZero()
			deadlineCurrent := deadlineSet && time.Now().Before(route.Deadline)
			// ResolveEnvironmentQualificationService checks both persisted leases
			// and returns their minimum deadline. Graph execution renews claims as
			// it advances through dependencies, so the request snapshots carried
			// in this context can be older than the authoritative route deadline.
			if !callerMatches || !targetMatches || !policyMatches || !deadlineCurrent {
				return fmt.Errorf("qualification service route rejected (caller=%t target=%t policy=%t current=%t): %w",
					callerMatches, targetMatches, policyMatches, deadlineCurrent, state.ErrConflict)
			}
		}
	}
	if !matchedCaller {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	return ctx.Err()
}

func qualificationGraphBindingPort(request state.EnvironmentWorkloadQualificationRequest) (int, error) {
	value, exists := request.FrozenInputs.Runtime["port"]
	if !exists {
		return 0, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	var port int
	if err := json.Unmarshal(value, &port); err != nil || port < 0 || port > 65535 {
		return 0, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	if port == 0 {
		port = api.DefaultAppPort
	}
	return port, nil
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
// URL at startup. It is retained for callers whose bindings share one transport.
func (e *Engine) WithEnvironmentQualificationServiceProxy(resolve func(context.Context, string) (string, error)) *Engine {
	if e == nil {
		return e
	}
	e.environmentQualificationServiceURL = resolve
	e.environmentQualificationServiceURLForTransport = nil
	return e
}

// WithEnvironmentQualificationServiceProxyForTransport supplies a node-local
// listener URL for each binding transport. Service-bound graphs are rejected
// before VM admission if the selected transport has no valid listener.
func (e *Engine) WithEnvironmentQualificationServiceProxyForTransport(
	resolve func(context.Context, string, api.ServiceBindingTransport) (string, error),
) *Engine {
	if e == nil {
		return e
	}
	e.environmentQualificationServiceURL = nil
	e.environmentQualificationServiceURLForTransport = resolve
	return e
}

func (e *Engine) hasEnvironmentQualificationServiceProxy() bool {
	return e != nil && (e.environmentQualificationServiceURL != nil || e.environmentQualificationServiceURLForTransport != nil)
}

func (e *Engine) resolveEnvironmentQualificationServiceURL(ctx context.Context, nodeID string,
	transport api.ServiceBindingTransport) (string, error) {
	if e == nil {
		return "", state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	if e.environmentQualificationServiceURLForTransport != nil {
		return e.environmentQualificationServiceURLForTransport(ctx, nodeID, transport)
	}
	if e.environmentQualificationServiceURL != nil {
		return e.environmentQualificationServiceURL(ctx, nodeID)
	}
	return "", state.ErrEnvironmentWorkloadPreparationUnavailable
}

func (e *Engine) prepareQualificationServiceBindings(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest, nodeID string, spec *AppSpec) error {
	if len(claimed.FrozenInputs.ServiceBindings) == 0 {
		return nil
	}
	if !e.hasEnvironmentQualificationServiceProxy() || ctx.Value(qualificationGraphContextKey{}) != claimed.GraphID {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	listener, ok := ctx.Value(qualificationGraphServiceListenerContextKey{}).(qualificationGraphServiceListener)
	if !ok || listener.nodeID != nodeID {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	transport := claimed.FrozenInputs.Baseline.EffectiveServiceBindingTransport()
	raw, ok := listener.bases[transport]
	if !ok {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	base, err := parseQualificationServiceBase(raw, transport)
	if err != nil {
		return err
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
			return fmt.Errorf("qualification service binding %q collides with an existing environment key %q: %w", name, binding.EnvKey, state.ErrConflict)
		}
		keys[binding.EnvKey] = true
		if transport == api.ServiceBindingTransportHTTPS {
			// The wildcard certificate authenticates one-label .internal names;
			// the node-local DNS alias is only discovery. The dedicated private
			// route still checks the caller's exact graph and binding.
			host := binding.Workload + ".internal"
			if !qualificationHTTPSBaseHostnameValid(host) {
				return state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			base.Host = host
		}
		base.Path = api.EnvironmentQualificationServicePrefix + claimed.GraphID + "/" + name
		spec.APIEnv = append(spec.APIEnv, fcvm.APIEnvEntry{Key: binding.EnvKey, Value: strings.TrimSuffix(base.String(), "/")})
	}
	return nil
}

func parseQualificationServiceBase(raw string, transport api.ServiceBindingTransport) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Path != "" || base.Opaque != "" {
		return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	switch transport {
	case api.ServiceBindingTransportHTTP:
		ip, parseErr := netip.ParseAddr(base.Hostname())
		if parseErr != nil || !ip.Is4() || !ip.IsPrivate() || base.Scheme != "http" || base.Port() != fmt.Sprint(api.ServiceBindingPort) {
			return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
		}
	case api.ServiceBindingTransportHTTPS:
		if base.Scheme != "https" || base.Port() != "443" || !qualificationHTTPSBaseHostnameValid(base.Hostname()) {
			return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
		}
	default:
		return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	return base, nil
}

func qualificationHTTPSBaseHostnameValid(host string) bool {
	if !strings.HasSuffix(strings.ToLower(host), ".internal") {
		return false
	}
	label := strings.TrimSuffix(strings.ToLower(host), ".internal")
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
