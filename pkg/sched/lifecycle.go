package sched

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceRolloutTimeout = 10 * time.Minute

func instanceModeForApp(app state.App) string {
	switch app.Manifest.ExecutionMode {
	case api.ExecutionModeService:
		return string(state.InstanceModeService)
	case api.ExecutionModeWorker:
		return string(state.InstanceModeWorker)
	case api.ExecutionModeJob:
		return string(state.InstanceModeJob)
	default:
		return string(state.InstanceModeNormal)
	}
}

func desiredServiceReplicas(manifest state.AppManifest) int {
	if manifest.ServiceReplicas == nil {
		return 1
	}
	return manifest.ServiceReplicas.Desired
}

// serviceReplicaStatus is the scheduler's readiness projection for one
// service deployment. A service target is about healthy serving capacity, not
// merely rows that still hold a RAM reservation:
//
//   - ready replicas are RUNNING after the boot readiness gate;
//   - starting replicas are still in WAKING/COLD_BOOTING;
//   - draining replicas are leaving service (SNAPSHOTTING/MIGRATING); and
//   - unavailable replicas are terminal or parked.
//
// The in-flight buckets are deliberately separate from ready. This prevents a
// scale-down from parking the last healthy replica just because a replacement
// is still booting, while still preventing the reconciler from admitting
// duplicate replacements for work already in flight.
type serviceReplicaStatus struct {
	ready       int
	starting    int
	draining    int
	unavailable int
}

func (s serviceReplicaStatus) inFlight() int {
	return s.starting + s.draining
}

func (s serviceReplicaStatus) managed() int {
	return s.ready + s.inFlight()
}

func normalizedInstanceMode(mode string) string {
	if mode == "" {
		return string(state.InstanceModeNormal)
	}
	return mode
}

func instanceModeUsesSnapshots(mode string) bool {
	switch state.InstanceMode(normalizedInstanceMode(mode)) {
	case state.InstanceModeWorker, state.InstanceModeJob:
		return false
	default:
		return true
	}
}

func instanceModeMatchesApp(app state.App, ins state.Instance) bool {
	return normalizedInstanceMode(ins.Mode) == instanceModeForApp(app)
}

// listServiceReplicas returns every service-mode row for a deployment,
// including terminal and parked history. The reconciler needs those states to
// distinguish an unavailable replica from a boot already in flight; filtering
// to CountsForConcurrency would collapse that distinction.
func listServiceReplicas(ctx context.Context, store state.Store, appID, deploymentID string) ([]state.Instance, error) {
	instances, err := store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	replicas := make([]state.Instance, 0, len(instances))
	for _, ins := range instances {
		if ins.DeploymentID == deploymentID && ins.Mode == string(state.InstanceModeService) {
			replicas = append(replicas, ins)
		}
	}
	return replicas, nil
}

func classifyServiceReplicas(replicas []state.Instance) serviceReplicaStatus {
	var status serviceReplicaStatus
	for _, replica := range replicas {
		switch state.State(replica.State) {
		case state.StateRunning:
			status.ready++
		case state.StateWaking, state.StateColdBooting:
			status.starting++
		case state.StateSnapshotting, state.StateMigrating:
			status.draining++
		default:
			status.unavailable++
		}
	}
	return status
}

// observeServiceReplicaStatus projects the current serving capacity of an
// app onto the scheduler metrics registry. Terminal and parked rows remain
// in the instance table for reconciliation and retention, so unavailable is
// derived as the desired-capacity shortfall after active ready, starting, and
// draining rows are counted rather than treating historical rows as live
// replicas.
func (e *Engine) observeServiceReplicaStatus(ctx context.Context, app state.App, deployments []state.Deployment) {
	if e.ops == nil {
		return
	}
	desired := 0
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		desired = desiredServiceReplicas(app.Manifest)
	}
	instances, err := e.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		e.log.Warn("sched: observe service replica status", "app", app.ID, "err", err)
		return
	}
	liveDeployments := make(map[string]struct{}, len(deployments))
	for _, deployment := range deployments {
		liveDeployments[deployment.ID] = struct{}{}
	}
	var status serviceReplicaStatus
	for _, instance := range instances {
		if instance.Mode != string(state.InstanceModeService) {
			continue
		}
		if _, ok := liveDeployments[instance.DeploymentID]; !ok {
			continue
		}
		switch state.State(instance.State) {
		case state.StateRunning:
			status.ready++
		case state.StateWaking, state.StateColdBooting:
			status.starting++
		case state.StateSnapshotting, state.StateMigrating:
			status.draining++
		}
	}
	status.unavailable = desired - status.ready - status.starting - status.draining
	if status.unavailable < 0 {
		status.unavailable = 0
	}
	e.ops.SetServiceReplicaStatus(app.ID, desired, status.ready, status.starting, status.draining, status.unavailable)
}

func listLiveDeploymentInstances(ctx context.Context, store state.Store, appID, deploymentID string) ([]state.Instance, error) {
	instances, err := store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	live := make([]state.Instance, 0, len(instances))
	for _, ins := range instances {
		if ins.DeploymentID != deploymentID || !state.State(ins.State).CountsForConcurrency() {
			continue
		}
		live = append(live, ins)
	}
	return live, nil
}

func detachedServiceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (e *Engine) scheduleServiceReconcile(ctx context.Context, deploymentID string) {
	if e == nil || e.store == nil || deploymentID == "" {
		return
	}
	go e.ReconcileServiceDeployment(detachedServiceContext(ctx), deploymentID)
}

func (e *Engine) serviceMutex(deploymentID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.serviceMu == nil {
		e.serviceMu = make(map[string]*sync.Mutex)
	}
	mu, ok := e.serviceMu[deploymentID]
	if !ok {
		mu = &sync.Mutex{}
		e.serviceMu[deploymentID] = mu
	}
	return mu
}

func (e *Engine) serviceAppMutex(appID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.serviceAppMu == nil {
		e.serviceAppMu = make(map[string]*sync.Mutex)
	}
	mu, ok := e.serviceAppMu[appID]
	if !ok {
		mu = &sync.Mutex{}
		e.serviceAppMu[appID] = mu
	}
	return mu
}

// allocateServiceReplicaTargets distributes one scope's app-level service
// target across its live deployment generations. Traffic weights are used as
// the allocation signal, but every positive-weight generation gets one
// replica when the target is large enough to support it. This keeps a small
// canary warm without allowing a second live generation to duplicate the
// full target.
//
// The caller supplies deployments in deterministic order (LiveDeployments is
// newest first). That order is the final tie-breaker for equal weights and
// equal remainders.
func allocateServiceReplicaTargets(deployments []state.Deployment, desired int) map[string]int {
	targets := make(map[string]int, len(deployments))
	for _, dep := range deployments {
		targets[dep.ID] = 0
	}
	if len(deployments) == 0 || desired <= 0 {
		return targets
	}
	if len(deployments) == 1 {
		targets[deployments[0].ID] = desired
		return targets
	}

	weights := make([]int64, len(deployments))
	positive := make([]int, 0, len(deployments))
	var totalWeight int64
	for i, dep := range deployments {
		if dep.TrafficPercent <= 0 {
			continue
		}
		weights[i] = int64(dep.TrafficPercent)
		totalWeight += weights[i]
		positive = append(positive, i)
	}
	if len(positive) == 0 || totalWeight <= 0 {
		// Corrupt or legacy traffic metadata must not make every generation
		// unavailable. LiveDeployments is newest first, so prefer the newest
		// generation while the traffic split is repaired.
		targets[deployments[0].ID] = desired
		return targets
	}

	if desired < len(positive) {
		// The target cannot give every generation a floor. Prefer the highest
		// traffic weights, with the stable input order as the tie-breaker.
		sort.SliceStable(positive, func(i, j int) bool {
			return weights[positive[i]] > weights[positive[j]]
		})
		for _, index := range positive[:desired] {
			targets[deployments[index].ID]++
		}
		return targets
	}

	for _, index := range positive {
		targets[deployments[index].ID] = 1
	}
	remaining := desired - len(positive)
	if remaining == 0 {
		return targets
	}

	type remainder struct {
		index  int
		value  int64
		weight int64
	}
	remainders := make([]remainder, 0, len(positive))
	assigned := 0
	for _, index := range positive {
		numerator := int64(remaining) * weights[index]
		whole := int(numerator / totalWeight)
		targets[deployments[index].ID] += whole
		assigned += whole
		remainders = append(remainders, remainder{
			index:  index,
			value:  numerator % totalWeight,
			weight: weights[index],
		})
	}

	// Largest remainder makes the integer allocation sum exactly to desired.
	// Higher traffic wins exact ties so a 25/75 split with four replicas is
	// allocated 1/3 rather than depending on map or database iteration order.
	sort.SliceStable(remainders, func(i, j int) bool {
		if remainders[i].value != remainders[j].value {
			return remainders[i].value > remainders[j].value
		}
		return remainders[i].weight > remainders[j].weight
	})
	for i := assigned; i < remaining; i++ {
		index := remainders[i-assigned].index
		targets[deployments[index].ID]++
	}
	return targets
}

func normalizedDeploymentScope(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}

// serviceReplicaTargets computes allocations independently per deployment
// scope. Canary generations in one scope share that scope's target; unrelated
// staging/prod scopes retain their own service capacity.
func (e *Engine) serviceReplicaTargets(ctx context.Context, app state.App, deployments []state.Deployment) (map[string]int, error) {
	rules, err := e.store.ListMirrorRules(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	mirrorDeployments := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		mirrorDeployments[rule.MirrorDeploymentID] = struct{}{}
	}

	byScope := make(map[string][]state.Deployment)
	for _, dep := range deployments {
		if _, mirror := mirrorDeployments[dep.ID]; mirror {
			continue
		}
		scope := normalizedDeploymentScope(dep.Scope)
		byScope[scope] = append(byScope[scope], dep)
	}

	targets := make(map[string]int, len(deployments))
	desired := desiredServiceReplicas(app.Manifest)
	for _, scoped := range byScope {
		for deploymentID, target := range allocateServiceReplicaTargets(scoped, desired) {
			targets[deploymentID] = target
		}
	}
	return targets, nil
}

func serviceRolloutScope(dep state.Deployment) string {
	return normalizedDeploymentScope(dep.Scope)
}

func activeServiceRollouts(deployments []state.Deployment) map[string]state.Deployment {
	rollouts := make(map[string]state.Deployment)
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || !state.IsServiceRollout(dep) {
			continue
		}
		scope := serviceRolloutScope(dep)
		current, ok := rollouts[scope]
		if !ok || dep.CreatedAt.After(current.CreatedAt) ||
			(dep.CreatedAt.Equal(current.CreatedAt) && dep.ID > current.ID) {
			rollouts[scope] = dep
		}
	}
	return rollouts
}

func previousServiceDeployment(rollout state.Deployment, deployments []state.Deployment) state.Deployment {
	var previous state.Deployment
	for _, dep := range deployments {
		if dep.ID == rollout.ID || dep.Status != state.DeployLive ||
			serviceRolloutScope(dep) != serviceRolloutScope(rollout) ||
			state.IsServiceRollout(dep) {
			continue
		}
		if !rollout.CreatedAt.IsZero() && dep.CreatedAt.After(rollout.CreatedAt) {
			continue
		}
		if previous.ID == "" || dep.CreatedAt.After(previous.CreatedAt) ||
			(dep.CreatedAt.Equal(previous.CreatedAt) && dep.ID > previous.ID) {
			previous = dep
		}
	}
	return previous
}

func serviceRolloutStartedAt(dep state.Deployment) time.Time {
	if dep.RolloutStartedAt != nil {
		return *dep.RolloutStartedAt
	}
	return dep.CreatedAt
}

func serviceRolloutTimedOut(dep state.Deployment, now time.Time) bool {
	started := serviceRolloutStartedAt(dep)
	return !started.IsZero() && now.Sub(started) >= serviceRolloutTimeout
}

func (e *Engine) emitServiceRolloutChange(ctx context.Context, appID, deploymentID string, status state.DeploymentStatus) {
	payload, _ := json.Marshal(map[string]any{
		"kind":          "service_rollout",
		"status":        string(status),
		"app_id":        appID,
		"deployment_id": deploymentID,
	})
	if err := e.Notifier().Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
		e.log.Warn("sched: notify service rollout change", "app", appID, "deployment", deploymentID, "status", status, "err", err)
	}
}

// drainServiceDeploymentInstances releases a generation after the traffic
// handoff. A successful generation is parked so its snapshot remains a useful
// rollback cache; an aborted, never-serving generation is hard-stopped.
func (e *Engine) drainServiceDeploymentInstances(ctx context.Context, deploymentID string, preserveSnapshot bool) {
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service rollout drain", "deployment", deploymentID, "err", err)
		}
		return
	}
	replicas, err := listServiceReplicas(ctx, e.store, dep.AppID, deploymentID)
	if err != nil {
		e.log.Warn("sched: list service rollout drain", "deployment", deploymentID, "err", err)
		return
	}
	for _, replica := range replicas {
		fresh, err := e.store.InstanceByID(ctx, replica.ID)
		if err != nil {
			continue
		}
		switch state.State(fresh.State) {
		case state.StateRunning:
			if preserveSnapshot {
				if err := e.Park(ctx, fresh.ID); err != nil {
					e.log.Warn("sched: park old service rollout replica", "instance", fresh.ID, "deployment", deploymentID, "err", err)
				}
				continue
			}
			if err := e.Evict(ctx, fresh.ID); err != nil {
				e.log.Warn("sched: stop aborted service rollout replica", "instance", fresh.ID, "deployment", deploymentID, "err", err)
			}
		case state.StateWaking, state.StateColdBooting:
			destroyCtx := context.WithoutCancel(ctx)
			if err := e.timedDestroy(destroyCtx, fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
				e.log.Warn("sched: destroy service rollout wake", "instance", fresh.ID, "deployment", deploymentID, "err", err)
				continue
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
		}
	}
}

// drainDeploymentInstances releases every serving instance for a deployment
// after a stable cutover (including request-mode/function instances). The
// service rollout helper above intentionally scopes to service replicas; a
// rollback of a hot request deployment needs the same lifecycle handoff or a
// one-instance plan can remain occupied by the superseded revision.
func (e *Engine) drainDeploymentInstances(ctx context.Context, deploymentID string, preserveSnapshot bool) {
	if e == nil || e.store == nil || deploymentID == "" {
		return
	}
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load deployment drain", "deployment", deploymentID, "err", err)
		}
		return
	}
	instances, err := e.store.ListInstancesForApp(ctx, dep.AppID)
	if err != nil {
		e.log.Warn("sched: list deployment drain", "deployment", deploymentID, "err", err)
		return
	}
	for _, candidate := range instances {
		if candidate.DeploymentID != deploymentID {
			continue
		}
		fresh, err := e.store.InstanceByID(ctx, candidate.ID)
		if err != nil {
			continue
		}
		switch state.State(fresh.State) {
		case state.StateRunning:
			if preserveSnapshot {
				if err := e.Park(ctx, fresh.ID); err != nil {
					e.log.Warn("sched: park superseded deployment instance", "instance", fresh.ID, "deployment", deploymentID, "err", err)
				}
			} else if err := e.Evict(ctx, fresh.ID); err != nil {
				e.log.Warn("sched: evict superseded deployment instance", "instance", fresh.ID, "deployment", deploymentID, "err", err)
			}
		case state.StateWaking, state.StateColdBooting:
			if err := e.timedDestroy(context.WithoutCancel(ctx), fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
				e.log.Warn("sched: destroy superseded deployment wake", "instance", fresh.ID, "deployment", deploymentID, "err", err)
				continue
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
		case state.StateSnapshotting:
			// An in-flight snapshot is already releasing the serving slot;
			// let it finish so the rollback cache remains valid.
		}
	}
}

type serviceRouteSubscriber interface {
	Subscribe(context.Context, []string) (<-chan db.Notification, error)
}

type serviceRouteGenerationStore interface {
	NextDeploymentRouteGeneration(context.Context) (int64, error)
}

func servingGatewayNames(nodes []state.ComputeNode) map[string]struct{} {
	expected := make(map[string]struct{})
	for _, node := range nodes {
		name := strings.TrimSpace(node.Name)
		role := ""
		if node.Role != nil {
			role = strings.TrimSpace(*node.Role)
		}
		if name == "" || !node.Active || (role != "compute-only" && role != "compute-node") ||
			node.GatewayTargetURL == nil || strings.TrimSpace(*node.GatewayTargetURL) == "" {
			continue
		}
		expected[name] = struct{}{}
	}
	return expected
}

func serviceRouteFleetConfigured(nodes []state.ComputeNode, ownerNodeID string) bool {
	if strings.TrimSpace(ownerNodeID) != "" {
		return true
	}
	for _, node := range nodes {
		name := strings.TrimSpace(node.Name)
		if name != "" && name != state.DefaultLocalNodeName {
			return true
		}
	}
	return false
}

func sortedServiceGatewaySet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) updateServiceRolloutHandoff(ctx context.Context, deploymentID string, mutate func(*state.ServiceRolloutHandoff)) {
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service rollout handoff", "deployment", deploymentID, "err", err)
		}
		return
	}
	handoff := dep.ServiceRolloutHandoff
	mutate(&handoff)
	now := time.Now().UTC()
	handoff.UpdatedAt = &now
	if _, err := e.store.UpdateServiceRolloutHandoff(ctx, deploymentID, handoff); err != nil &&
		!errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
		e.log.Warn("sched: persist service rollout handoff", "deployment", deploymentID, "phase", handoff.Phase, "err", err)
	}
}

func (e *Engine) failServiceRolloutHandoff(ctx context.Context, deploymentID, phase, reason string, missing []string) {
	e.updateServiceRolloutHandoff(ctx, deploymentID, func(h *state.ServiceRolloutHandoff) {
		h.Phase = phase
		h.LastError = reason
		h.MissingGateways = append([]string(nil), missing...)
	})
}

// waitForServiceRouteConvergence publishes a unique routing generation and
// waits for every registered serving gateway to confirm that both its weight
// table and live targets reflect the cutover. The caller has already retained
// the predecessor as a live zero-weight generation, so every failure path is
// safe: old requests continue while reconciliation retries.
func (e *Engine) waitForServiceRouteConvergence(ctx context.Context, appID, deploymentID string) (acknowledgedAt time.Time, fleetBarrier bool, ok bool) {
	return e.waitForServiceRouteConvergenceForRollout(ctx, appID, deploymentID, deploymentID, state.ServiceRolloutActionPromote)
}

func (e *Engine) waitForServiceRouteConvergenceForRollout(ctx context.Context, appID, rolloutID, deploymentID, action string) (acknowledgedAt time.Time, fleetBarrier bool, ok bool) {
	started := time.Now()
	nodes, err := e.store.ListComputeNodes(ctx, false)
	if err != nil {
		e.log.Warn("sched: list serving gateways for rollout", "app", appID, "deployment", deploymentID, "err", err)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "list_serving_gateways_failed", nil)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, false, false
	}
	expected := servingGatewayNames(nodes)
	if len(expected) == 0 {
		if serviceRouteFleetConfigured(nodes, e.ownerNodeID) {
			e.log.Warn("sched: no active serving gateways registered for rollout", "app", appID, "deployment", deploymentID)
			e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "no_active_serving_gateways", nil)
			e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "timeout", time.Since(started).Seconds())
			return time.Time{}, true, false
		}
		// Legacy single-box and unit-test installs have no registered gateway
		// identity. Preserve their existing notify path without pretending it
		// provides a fleet acknowledgement or telemetry drain barrier.
		e.emitServiceRolloutChange(ctx, appID, deploymentID, state.DeployLive)
		now := time.Now().UTC()
		e.updateServiceRolloutHandoff(ctx, rolloutID, func(h *state.ServiceRolloutHandoff) {
			h.Phase = state.ServiceRolloutPhaseDraining
			h.LastError = ""
			h.AcknowledgedAt = &now
		})
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "success", time.Since(started).Seconds())
		return now, false, true
	}
	expectedNames := sortedServiceGatewaySet(expected)
	subscriber, available := e.notif.(serviceRouteSubscriber)
	if !available {
		e.log.Warn("sched: deployment route acknowledgement subscriber unavailable", "app", appID, "deployment", deploymentID)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_ack_subscriber_unavailable", expectedNames)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, true, false
	}
	allocator, available := e.store.(serviceRouteGenerationStore)
	if !available {
		e.log.Warn("sched: deployment route generation store unavailable", "app", appID, "deployment", deploymentID)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_generation_store_unavailable", expectedNames)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, true, false
	}
	generation, err := allocator.NextDeploymentRouteGeneration(ctx)
	if err != nil {
		e.log.Warn("sched: allocate deployment route generation", "app", appID, "deployment", deploymentID, "err", err)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_generation_allocation_failed", expectedNames)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, true, false
	}
	e.updateServiceRolloutHandoff(ctx, rolloutID, func(h *state.ServiceRolloutHandoff) {
		h.Phase = state.ServiceRolloutPhaseRouting
		h.Generation = generation
		h.ExpectedGateways = append([]string(nil), expectedNames...)
		h.AcknowledgedGateways = nil
		h.MissingGateways = append([]string(nil), expectedNames...)
		h.LastError = ""
	})
	payloadBytes, err := json.Marshal(db.DeploymentRouteChangedPayload{
		AppID: appID, DeploymentID: deploymentID, Generation: generation,
	})
	if err != nil {
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_payload_encode_failed", expectedNames)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, true, false
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(api.ServiceRouteConvergenceTimeoutSeconds)*time.Second)
	defer cancel()
	events, err := subscriber.Subscribe(waitCtx, []string{db.NotifyDeploymentRouteAck})
	if err != nil {
		e.log.Warn("sched: subscribe deployment route acknowledgements", "app", appID, "deployment", deploymentID, "generation", generation, "err", err)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_ack_subscribe_failed", expectedNames)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, true, false
	}
	payload := string(payloadBytes)
	if err := e.Notifier().Notify(waitCtx, db.NotifyDeploymentRouteChanged, payload); err != nil {
		e.log.Warn("sched: publish deployment route generation", "app", appID, "deployment", deploymentID, "generation", generation, "err", err)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_publish_failed", expectedNames)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
		return time.Time{}, true, false
	}
	retry := time.NewTicker(time.Duration(api.ServiceRouteNotificationRetryMilliseconds) * time.Millisecond)
	defer retry.Stop()
	seen := make(map[string]struct{}, len(expected))
	for len(seen) < len(expected) {
		select {
		case <-waitCtx.Done():
			missing := make([]string, 0, len(expected)-len(seen))
			for node := range expected {
				if _, found := seen[node]; !found {
					missing = append(missing, node)
				}
			}
			sort.Strings(missing)
			e.log.Warn("sched: deployment route convergence incomplete", "app", appID, "deployment", deploymentID, "generation", generation, "missing", strings.Join(missing, ","))
			e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_convergence_timeout", missing)
			e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "timeout", time.Since(started).Seconds())
			return time.Time{}, true, false
		case event, open := <-events:
			if !open {
				e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseRouting, "route_ack_stream_closed", expectedNames)
				e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "error", time.Since(started).Seconds())
				return time.Time{}, true, false
			}
			ack, parseErr := db.ParseDeploymentRouteAckPayload(event.Payload)
			if parseErr != nil || ack.Generation != generation {
				continue
			}
			if _, wanted := expected[ack.Node]; wanted {
				seen[ack.Node] = struct{}{}
				missing := make(map[string]struct{}, len(expected)-len(seen))
				for node := range expected {
					if _, ok := seen[node]; !ok {
						missing[node] = struct{}{}
					}
				}
				acknowledged := sortedServiceGatewaySet(seen)
				missingNames := sortedServiceGatewaySet(missing)
				e.updateServiceRolloutHandoff(ctx, rolloutID, func(h *state.ServiceRolloutHandoff) {
					h.AcknowledgedGateways = acknowledged
					h.MissingGateways = missingNames
				})
			}
		case <-retry.C:
			_ = e.Notifier().Notify(waitCtx, db.NotifyDeploymentRouteChanged, payload)
		}
	}
	now := time.Now().UTC()
	e.updateServiceRolloutHandoff(ctx, rolloutID, func(h *state.ServiceRolloutHandoff) {
		h.Phase = state.ServiceRolloutPhaseDraining
		h.LastError = ""
		h.MissingGateways = nil
		h.AcknowledgedAt = &now
	})
	e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseRouting, "success", time.Since(started).Seconds())
	return now, true, true
}

// waitForServiceDeploymentDrain requires post-ack, fresh telemetry showing a
// quiet zero-inflight window for every predecessor replica. Missing or stale
// telemetry fails safe and keeps the predecessor resident.
func (e *Engine) waitForServiceDeploymentDrain(ctx context.Context, appID, rolloutID, deploymentID, action string, acknowledgedAt time.Time) bool {
	started := time.Now()
	replicas, err := listServiceReplicas(ctx, e.store, appID, deploymentID)
	if err != nil {
		e.log.Warn("sched: list predecessor replicas for drain", "app", appID, "deployment", deploymentID, "err", err)
		e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseDraining, "list_drain_replicas_failed", nil)
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseDraining, "error", time.Since(started).Seconds())
		return false
	}
	ids := make([]string, 0, len(replicas))
	for _, replica := range replicas {
		if state.State(replica.State) == state.StateRunning {
			ids = append(ids, replica.ID)
		}
	}
	if len(ids) == 0 {
		e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseDraining, "success", time.Since(started).Seconds())
		return true
	}
	drainCtx, cancel := context.WithTimeout(ctx, time.Duration(api.ServiceReplicaDrainTimeoutSeconds)*time.Second)
	defer cancel()
	quietFor := time.Duration(api.ServiceReplicaDrainQuietSeconds) * time.Second
	zeroSince := make(map[string]time.Time, len(ids))
	ticker := time.NewTicker(time.Duration(api.ServiceReplicaDrainPollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	for {
		now := time.Now()
		allQuiet := true
		for _, instanceID := range ids {
			fresh, loadErr := e.store.InstanceByID(drainCtx, instanceID)
			if errors.Is(loadErr, state.ErrNotFound) || (loadErr == nil && state.State(fresh.State) != state.StateRunning) {
				continue
			}
			if loadErr != nil {
				allQuiet = false
				continue
			}
			inflight, receivedAt, observed := e.telemetryCache.LookupInflightRequests(instanceID, now)
			if !observed || !receivedAt.After(acknowledgedAt) {
				delete(zeroSince, instanceID)
				allQuiet = false
				continue
			}
			if inflight > 0 {
				delete(zeroSince, instanceID)
				allQuiet = false
				continue
			}
			started, seenZero := zeroSince[instanceID]
			if !seenZero {
				zeroSince[instanceID] = receivedAt
				allQuiet = false
				continue
			}
			if receivedAt.Sub(started) < quietFor {
				allQuiet = false
			}
		}
		if allQuiet {
			e.updateServiceRolloutHandoff(ctx, rolloutID, func(h *state.ServiceRolloutHandoff) { h.LastError = "" })
			e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseDraining, "success", time.Since(started).Seconds())
			return true
		}
		select {
		case <-drainCtx.Done():
			e.log.Warn("sched: predecessor request drain incomplete", "app", appID, "deployment", deploymentID)
			e.failServiceRolloutHandoff(ctx, rolloutID, state.ServiceRolloutPhaseDraining, "request_drain_timeout", nil)
			e.ops.ObserveServiceRolloutHandoffPhase(action, state.ServiceRolloutPhaseDraining, "timeout", time.Since(started).Seconds())
			return false
		case <-ticker.C:
		}
	}
}

func (e *Engine) finishServiceRollout(ctx context.Context, app state.App, rollout, previous state.Deployment) bool {
	if previous.ID != "" {
		if _, err := e.store.BeginServiceRolloutCutover(ctx, rollout.ID); err != nil {
			if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
				e.log.Warn("sched: begin service rollout cutover", "app", app.ID, "deployment", rollout.ID, "err", err)
			}
			return false
		}
		acknowledgedAt, fleetBarrier, converged := e.waitForServiceRouteConvergenceForRollout(ctx, app.ID, rollout.ID, rollout.ID, state.ServiceRolloutActionPromote)
		if !converged {
			return false
		}
		if fleetBarrier && !e.waitForServiceDeploymentDrain(ctx, app.ID, rollout.ID, previous.ID, state.ServiceRolloutActionPromote, acknowledgedAt) {
			return false
		}
	}
	updated, err := e.store.FinalizeServiceRollout(ctx, rollout.ID)
	if err != nil {
		if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: finalize service rollout", "app", app.ID, "deployment", rollout.ID, "err", err)
		}
		return false
	}
	if previous.ID != "" {
		e.drainServiceDeploymentInstances(ctx, previous.ID, true)
	}
	e.emitServiceRolloutChange(ctx, app.ID, updated.ID, updated.Status)
	return true
}

func (e *Engine) reverseServiceRollout(ctx context.Context, app state.App, rollout state.Deployment) bool {
	updated, err := e.store.BeginServiceRolloutAbort(ctx, rollout.ID)
	if err != nil {
		if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: begin service rollout abort", "app", app.ID, "deployment", rollout.ID, "err", err)
		}
		return false
	}
	predecessorID := updated.ServiceRolloutHandoff.PredecessorDeploymentID
	if predecessorID == "" {
		e.failServiceRolloutHandoff(ctx, rollout.ID, state.ServiceRolloutPhaseRouting, "predecessor_missing", nil)
		return false
	}
	acknowledgedAt, fleetBarrier, converged := e.waitForServiceRouteConvergenceForRollout(ctx, app.ID, rollout.ID, predecessorID, state.ServiceRolloutActionAbort)
	if !converged {
		return false
	}
	if fleetBarrier && !e.waitForServiceDeploymentDrain(ctx, app.ID, rollout.ID, rollout.ID, state.ServiceRolloutActionAbort, acknowledgedAt) {
		return false
	}
	final, err := e.store.AbortServiceRollout(ctx, rollout.ID, updated.ServiceRolloutHandoff.Reason)
	if err != nil {
		if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: finalize service rollout abort", "app", app.ID, "deployment", rollout.ID, "err", err)
		}
		return false
	}
	e.drainServiceDeploymentInstances(ctx, rollout.ID, false)
	e.emitServiceRolloutChange(ctx, app.ID, predecessorID, state.DeployLive)
	e.emitServiceRolloutChange(ctx, app.ID, final.ID, final.Status)
	return true
}

func (e *Engine) abortServiceRollout(ctx context.Context, app state.App, rollout state.Deployment, reason string) bool {
	updated, err := e.store.AbortServiceRollout(ctx, rollout.ID, reason)
	if err != nil {
		if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: abort service rollout", "app", app.ID, "deployment", rollout.ID, "err", err)
		}
		return false
	}
	e.drainServiceDeploymentInstances(ctx, rollout.ID, false)
	live, listErr := e.store.LiveDeployments(ctx, app.ID)
	if listErr == nil && len(live) > 0 {
		e.emitServiceRolloutChange(ctx, app.ID, live[0].ID, live[0].Status)
	} else {
		e.emitServiceRolloutChange(ctx, app.ID, updated.ID, updated.Status)
	}
	return true
}

// reconcileServiceRollout advances one scope by at most one ready replica per
// pass. The old generation keeps the remainder of the desired capacity until
// the new generation proves readiness; the total temporary surge is one.
func (e *Engine) reconcileServiceRollout(ctx context.Context, app state.App, rollout state.Deployment, deployments []state.Deployment) {
	if rollout.ServiceRolloutHandoff.ActiveAbort() {
		e.reverseServiceRollout(ctx, app, rollout)
		return
	}
	desired := desiredServiceReplicas(app.Manifest)
	previous := previousServiceDeployment(rollout, deployments)
	replicas, err := listServiceReplicas(ctx, e.store, app.ID, rollout.ID)
	if err != nil {
		e.log.Warn("sched: list new service rollout replicas", "app", app.ID, "deployment", rollout.ID, "err", err)
		return
	}
	status := classifyServiceReplicas(replicas)
	if status.ready >= desired {
		e.finishServiceRollout(ctx, app, rollout, previous)
		return
	}
	if serviceRolloutTimedOut(rollout, time.Now().UTC()) {
		e.abortServiceRollout(ctx, app, rollout, "readiness timeout")
		return
	}
	if previous.ID == "" {
		e.convergeServiceReplicasToTarget(ctx, rollout.ID, desired, true)
	} else {
		newTarget := status.ready + 1
		if newTarget > desired {
			newTarget = desired
		}
		oldTarget := desired - status.ready
		if oldTarget < 0 {
			oldTarget = 0
		}
		e.convergeServiceReplicasToTarget(ctx, previous.ID, oldTarget, false)
		e.convergeServiceReplicasToTarget(ctx, rollout.ID, newTarget, true)
	}
	// Admission may synchronously reach RUNNING. Re-read so a fast boot can
	// complete the rollout without waiting for a second notification.
	ready, readErr := listServiceReplicas(ctx, e.store, app.ID, rollout.ID)
	if readErr == nil && classifyServiceReplicas(ready).ready >= desired {
		e.finishServiceRollout(ctx, app, rollout, previous)
	}
}

// ReconcileServiceDeployment restores the app's service allocation after a
// deployment or instance notification. The allocation is app-scoped because
// a canary and its predecessor are both live during a rollout.
func (e *Engine) ReconcileServiceDeployment(ctx context.Context, deploymentID string) {
	ctx = detachedServiceContext(ctx)
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service deployment for app reconcile", "deployment", deploymentID, "err", err)
		}
		return
	}
	e.ReconcileServiceApp(ctx, dep.AppID)
}

// ReconcileServiceApp applies one globally consistent service allocation to
// every live deployment of an app. Steady-state surplus is parked before a
// deficit is admitted. Active rollout scopes are handled separately above and
// never park healthy predecessor capacity merely to make candidate headroom.
func (e *Engine) ReconcileServiceApp(ctx context.Context, appID string) {
	ctx = detachedServiceContext(ctx)
	reconcileMu := e.serviceAppMutex(appID)
	reconcileMu.Lock()
	defer reconcileMu.Unlock()

	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service app", "app", appID, "err", err)
		}
		return
	}
	if !e.ownsApp(app) {
		return
	}
	if app.Status != state.AppActive {
		return
	}
	deployments, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		e.log.Warn("sched: list live service deployments", "app", appID, "err", err)
		return
	}
	defer e.observeServiceReplicaStatus(ctx, app, deployments)
	handledScopes := make(map[string]struct{})
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		rollouts := activeServiceRollouts(deployments)
		scopes := make([]string, 0, len(rollouts))
		for scope := range rollouts {
			scopes = append(scopes, scope)
		}
		sort.Strings(scopes)
		for _, scope := range scopes {
			handledScopes[scope] = struct{}{}
			e.reconcileServiceRollout(ctx, app, rollouts[scope], deployments)
		}
	}
	targets := make(map[string]int, len(deployments))
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		var targetErr error
		targets, targetErr = e.serviceReplicaTargets(ctx, app, deployments)
		if targetErr != nil {
			e.log.Warn("sched: allocate service replicas", "app", appID, "err", targetErr)
			return
		}
		for _, dep := range deployments {
			if _, handled := handledScopes[serviceRolloutScope(dep)]; handled {
				delete(targets, dep.ID)
			}
		}
	} else {
		// A mode switch away from service still needs to drain the old
		// service rows. Non-service instances are intentionally ignored by
		// convergeServiceReplicasToTarget's service-row filter.
		for _, dep := range deployments {
			targets[dep.ID] = 0
		}
	}
	// First release capacity from generations above their allocation.
	for _, dep := range deployments {
		target, ok := targets[dep.ID]
		if !ok {
			continue
		}
		e.convergeServiceReplicasToTarget(ctx, dep.ID, target, false)
	}
	// Then fill deficits with the capacity made available above.
	for _, dep := range deployments {
		target, ok := targets[dep.ID]
		if !ok {
			continue
		}
		e.convergeServiceReplicasToTarget(ctx, dep.ID, target, true)
	}
}

func (e *Engine) convergeServiceReplicas(ctx context.Context, deploymentID string) {
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service deployment", "deployment", deploymentID, "err", err)
		}
		return
	}
	app, err := e.store.AppByID(ctx, dep.AppID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service app", "app", dep.AppID, "deployment", deploymentID, "err", err)
		}
		return
	}
	if app.Status != state.AppActive {
		return
	}
	deployments, err := e.store.LiveDeployments(ctx, dep.AppID)
	if err != nil {
		e.log.Warn("sched: list live service deployments", "app", dep.AppID, "err", err)
		return
	}
	targets := make(map[string]int, len(deployments))
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		var targetErr error
		targets, targetErr = e.serviceReplicaTargets(ctx, app, deployments)
		if targetErr != nil {
			e.log.Warn("sched: allocate service replicas", "app", dep.AppID, "err", targetErr)
			return
		}
	} else {
		for _, liveDep := range deployments {
			targets[liveDep.ID] = 0
		}
	}
	desired, ok := targets[deploymentID]
	if !ok {
		return
	}
	e.convergeServiceReplicasToTarget(ctx, deploymentID, desired, true)
}

func (e *Engine) convergeServiceReplicasToTarget(ctx context.Context, deploymentID string, desired int, admit bool) {
	if desired < 0 {
		desired = 0
	}
	reconcileMu := e.serviceMutex(deploymentID)
	reconcileMu.Lock()
	defer reconcileMu.Unlock()

	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service deployment", "deployment", deploymentID, "err", err)
		}
		return
	}
	if dep.Status != state.DeployLive {
		return
	}
	app, err := e.store.AppByID(ctx, dep.AppID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service app", "app", dep.AppID, "deployment", deploymentID, "err", err)
		}
		return
	}
	if app.Status != state.AppActive {
		return
	}
	serviceMode := instanceModeForApp(app) == string(state.InstanceModeService)
	if serviceMode {
		mirror, mirrorErr := e.isMirrorDeployment(ctx, dep.AppID, dep.ID)
		if mirrorErr != nil {
			e.log.Warn("sched: check service mirror deployment", "app", dep.AppID, "deployment", dep.ID, "err", mirrorErr)
			return
		}
		if mirror {
			return
		}
	}
	if serviceMode {
		instances, listErr := listLiveDeploymentInstances(ctx, e.store, dep.AppID, dep.ID)
		if listErr != nil {
			e.log.Warn("sched: list incompatible service instances", "deployment", dep.ID, "err", listErr)
			return
		}
		e.drainIncompatibleServiceReplicas(ctx, instances)
	}
	serviceReplicas, err := listServiceReplicas(ctx, e.store, dep.AppID, deploymentID)
	if err != nil {
		e.log.Warn("sched: list service replicas", "deployment", deploymentID, "err", err)
		return
	}
	status := classifyServiceReplicas(serviceReplicas)
	// Never trade away healthy capacity while a replacement is still
	// booting. A deployment can temporarily exceed desired during a
	// scale-down, but it must not temporarily fall below desired because a
	// not-yet-ready replica was counted as equivalent to RUNNING.
	if excess := status.ready - desired; excess > 0 {
		parked := e.parkSurplusServiceReplicas(ctx, serviceReplicas, excess)
		status.ready -= parked
	}
	if !admit || desired <= 0 {
		return
	}
	// Service capacity is a failure-isolation contract. Do not let the
	// app-level sticky-warm hint place every replica on the same compute node;
	// each admission still uses the normal capacity and ledger gates, but the
	// chooser sees current fleet headroom so replicas spread across the
	// available nodes.
	ctx = withServiceReplicaPlacementSpread(ctx)
	for status.managed() < desired {
		result, admitErr := e.AdmitInstanceForDeployment(
			ctx, dep.AppID, dep.ID, dep.Scope, TriggerServiceReplica,
		)
		if admitErr != nil {
			e.log.Warn("sched: admit service replica", "app", dep.AppID, "deployment", dep.ID, "err", admitErr)
			return
		}
		if result.AtCapacity {
			e.log.Debug("sched: service replica admission at capacity", "app", dep.AppID, "deployment", dep.ID)
			return
		}
		// AdmitInstanceForDeployment returns only after the new replica has
		// reached RUNNING (or has failed), so count the successful result as
		// ready for this pass. A later asynchronous transition will trigger
		// another reconciliation for failures and replacements.
		status.ready++
	}
}

// scheduleWorkerReconcile restores the worker allocation after an
// infrastructure failure without coupling explicit StopInstance calls to an
// automatic restart. Customer-requested stops remain stops; dead-node,
// liveness, and deployment lifecycle paths call this helper explicitly. The
// app-scoped reconciler derives a queue-backed pool target when configured and
// otherwise preserves the singleton contract.
func (e *Engine) scheduleWorkerReconcile(ctx context.Context, deploymentID string) {
	if e == nil || e.store == nil || deploymentID == "" {
		return
	}
	go e.ReconcileWorkerDeployment(detachedServiceContext(ctx), deploymentID)
}

// ReconcileWorkerDeployment resolves the app for an instance/deployment
// notification and delegates to the app-scoped singleton reconciler.
func (e *Engine) ReconcileWorkerDeployment(ctx context.Context, deploymentID string) {
	ctx = detachedServiceContext(ctx)
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load worker deployment for app reconcile", "deployment", deploymentID, "err", err)
		}
		return
	}
	e.ReconcileWorkerApp(ctx, dep.AppID)
}

// workerDeploymentTargets selects one live generation per deployment scope.
// LiveDeployments is newest-first; the explicit sort keeps the selection
// deterministic for alternate Store implementations and equal timestamps.
func workerDeploymentTargets(deployments []state.Deployment) map[string]struct{} {
	ordered := append([]state.Deployment(nil), deployments...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].ID > ordered[j].ID
		}
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})
	targets := make(map[string]struct{})
	seenScopes := make(map[string]struct{})
	for _, dep := range ordered {
		if dep.Status != state.DeployLive {
			continue
		}
		scope := normalizedDeploymentScope(dep.Scope)
		if _, exists := seenScopes[scope]; exists {
			continue
		}
		seenScopes[scope] = struct{}{}
		targets[dep.ID] = struct{}{}
	}
	return targets
}

func workerStatePreference(ins state.Instance) int {
	switch state.State(ins.State) {
	case state.StateRunning:
		return 0
	case state.StateWaking, state.StateColdBooting:
		return 1
	default:
		return 2
	}
}

// workerQueueDepth returns the queue signal used by the worker reconciler.
// Enabled queue bindings are aggregated when present; an app with no bindings
// retains the legacy app-wide queue projection. Keeping this read in the
// reconciler means an instance transition cannot accidentally collapse a
// queue-sized worker fleet back to the singleton target.
func (e *Engine) workerQueueDepth(ctx context.Context, app state.App) (int, error) {
	if e.brokerLag != nil {
		lag, ok, err := e.brokerLag.BrokerLag(ctx, app.ID)
		if err != nil {
			return 0, err
		}
		if ok {
			return int(lag), nil
		}
	}
	bindings, err := e.store.ListQueueBindingsForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return 0, err
	}
	if len(bindings) == 0 {
		stats, statsErr := e.store.QueueState(ctx, app.ID)
		if statsErr != nil {
			return 0, statsErr
		}
		return stats.Depth, nil
	}
	depth := 0
	for _, binding := range bindings {
		if !binding.Enabled {
			continue
		}
		stats, statsErr := e.store.QueueStateForQueue(ctx, app.ID, binding.QueueName)
		if statsErr != nil {
			return 0, statsErr
		}
		depth += stats.Depth
	}
	return depth, nil
}

// workerReplicaTarget derives the resident worker count from the queue-depth
// policy and the plan/app ceilings. Worker mode intentionally retains one
// resident worker when the queue is empty: this is the long-lived consumer
// contract, while request-mode scale-to-zero remains unchanged. A caller may
// provide an already-computed desired value from the queue trigger; the
// durable policy/queue read remains the fallback for lifecycle notifications.
func (e *Engine) workerReplicaTarget(ctx context.Context, app state.App, override *int) int {
	account, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		e.log.Warn("sched: load worker account limits", "app", app.ID, "err", err)
		return 1
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.WorkerReplicasMax <= 0 {
		return 0
	}

	max := app.MaxConcurrency
	if max <= 0 || max > limits.MaxConcurrency {
		max = limits.MaxConcurrency
	}
	if app.ScalingPolicy != nil && app.ScalingPolicy.MaxInstances > 0 && app.ScalingPolicy.MaxInstances < max {
		max = app.ScalingPolicy.MaxInstances
	}
	if limits.WorkerReplicasMax < max {
		max = limits.WorkerReplicasMax
	}
	if max <= 0 {
		return 0
	}

	minAllowed := 1
	if app.Manifest.WorkerReplicas != nil && app.Manifest.WorkerReplicas.Min == 0 {
		minAllowed = 0
	}
	desired := minAllowed
	if override != nil {
		desired = *override
	} else if policy := app.ScalingPolicy; policy != nil && policy.Target != nil && (policy.Target.Metric == "queue_depth" || policy.Target.Metric == "queue_lag") && policy.Target.Value > 0 {
		depth, depthErr := e.workerQueueDepth(ctx, app)
		if depthErr != nil {
			e.log.Warn("sched: read worker queue depth", "app", app.ID, "err", depthErr)
		} else if depth > 0 {
			desired = int(math.Ceil(float64(depth) / policy.Target.Value))
		} else {
			desired = 0
		}
		if policy.MinInstances > desired {
			desired = policy.MinInstances
		}
	}
	if desired < minAllowed {
		desired = minAllowed
	}
	if desired > max {
		desired = max
	}
	return desired
}

// capWorkerReplicasToAccount applies the plan's account-wide worker budget to
// this app's target. The normal app ledger still enforces max_concurrency; the
// additional count is needed because WorkerReplicasMax is intentionally a
// cross-app plan limit.
func (e *Engine) capWorkerReplicasToAccount(ctx context.Context, app state.App, current []state.Instance, desired int) int {
	if desired <= 0 {
		return desired
	}
	account, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		e.log.Warn("sched: load worker account capacity", "app", app.ID, "err", err)
		return desired
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.WorkerReplicasMax <= 0 {
		return 0
	}
	all, err := e.store.ListInstancesForAccount(ctx, app.AccountID)
	if err != nil {
		e.log.Warn("sched: list account workers", "app", app.ID, "err", err)
		return desired
	}
	currentAppWorkers := 0
	for _, ins := range current {
		if state.State(ins.State).CountsForConcurrency() {
			currentAppWorkers++
		}
	}
	totalWorkers := 0
	for _, ins := range all {
		if ins.Mode == string(state.InstanceModeWorker) && state.State(ins.State).CountsForConcurrency() {
			totalWorkers++
		}
	}
	available := limits.WorkerReplicasMax - (totalWorkers - currentAppWorkers)
	if available < 0 {
		available = 0
	}
	if desired > available {
		desired = available
	}
	return desired
}

// ReconcileWorkerApp converges worker mode to the queue-derived resident count
// for the newest live deployment in each scope. With no queue-depth policy the
// target remains one, preserving the original worker singleton behavior. It
// also drains worker rows after a mode switch, failed activation, or supersede.
// The same app-level mutex used by service allocation serializes mode switches
// without nesting appMu around admission or graceful stop calls.
func (e *Engine) ReconcileWorkerApp(ctx context.Context, appID string) {
	e.reconcileWorkerApp(ctx, appID, nil)
}

// ReconcileWorkerPool applies a queue trigger's desired worker count. It is a
// narrow optional engine surface used by pkg/sched/targets: unlike request
// wakes, worker admissions go through the explicit deployment path so the
// request wake gate cannot reject a legitimate queue-driven scale-out.
func (e *Engine) ReconcileWorkerPool(ctx context.Context, appID string, desired int, trigger string) error {
	if trigger == "" {
		trigger = TriggerWorkerPool
	}
	e.reconcileWorkerAppWithTrigger(ctx, appID, &desired, trigger)
	return nil
}

// parseStopSignal parses a string signal representation (e.g. "SIGTERM", "TERM", "15",
// "SIGINT", "INT", "2", "SIGQUIT", "SIGKILL", etc.) into a syscall.Signal.
// Empty or unrecognised values fall back to SIGTERM.
func parseStopSignal(s string) syscall.Signal {
	s = strings.TrimSpace(strings.ToUpper(s))
	switch s {
	case "":
		return syscall.SIGTERM
	case "SIGTERM", "TERM", "15":
		return syscall.SIGTERM
	case "SIGINT", "INT", "2":
		return syscall.SIGINT
	case "SIGQUIT", "QUIT", "3":
		return syscall.SIGQUIT
	case "SIGHUP", "HUP", "1":
		return syscall.SIGHUP
	case "SIGUSR1", "USR1", "10":
		return syscall.SIGUSR1
	case "SIGUSR2", "USR2", "12":
		return syscall.SIGUSR2
	default:
		return syscall.SIGTERM
	}
}

// workerStopOptions returns StopOptions for a worker instance based on the app manifest.
func (e *Engine) workerStopOptions(app state.App) StopOptions {
	grace := app.Manifest.StopGracePeriodS
	if grace <= 0 {
		grace = 30
	}
	sig := parseStopSignal(app.Manifest.StopSignal)
	return StopOptions{
		Signal:       int32(sig),
		GraceSeconds: int32(grace),
	}
}

func (e *Engine) reconcileWorkerApp(ctx context.Context, appID string, desiredOverride *int) {
	e.reconcileWorkerAppWithTrigger(ctx, appID, desiredOverride, TriggerWorkerSingleton)
}

func (e *Engine) reconcileWorkerAppWithTrigger(ctx context.Context, appID string, desiredOverride *int, trigger string) {
	ctx = detachedServiceContext(ctx)
	reconcileMu := e.serviceAppMutex(appID)
	reconcileMu.Lock()
	defer reconcileMu.Unlock()

	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load worker app", "app", appID, "err", err)
		}
		return
	}
	if !e.ownsApp(app) {
		return
	}
	deployments, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		e.log.Warn("sched: list live worker deployments", "app", appID, "err", err)
		return
	}
	targets := make(map[string]struct{})
	if app.Status == state.AppActive && instanceModeForApp(app) == string(state.InstanceModeWorker) {
		targets = workerDeploymentTargets(deployments)
	}
	desired := 0
	if len(targets) > 0 {
		desired = e.workerReplicaTarget(ctx, app, desiredOverride)
	}

	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		e.log.Warn("sched: list worker instances", "app", appID, "err", err)
		return
	}
	workers := make([]state.Instance, 0, len(instances))
	for _, ins := range instances {
		if ins.Mode == string(state.InstanceModeWorker) && state.State(ins.State).CountsForRAM() {
			workers = append(workers, ins)
		}
	}
	sort.SliceStable(workers, func(i, j int) bool {
		left, right := workerStatePreference(workers[i]), workerStatePreference(workers[j])
		if left != right {
			return left < right
		}
		if workers[i].StartedAt.Equal(workers[j].StartedAt) {
			return workers[i].ID < workers[j].ID
		}
		return workers[i].StartedAt.Before(workers[j].StartedAt)
	})

	stopOpts := e.workerStopOptions(app)
	kept := make(map[string]int, len(targets))
	for _, ins := range workers {
		_, wanted := targets[ins.DeploymentID]
		if wanted && kept[ins.DeploymentID] < desired && state.State(ins.State).CountsForConcurrency() {
			kept[ins.DeploymentID]++
			continue
		}
		if err := e.stopManagedWorker(ctx, ins.ID, stopOpts); err != nil {
			e.log.Warn("sched: drain surplus worker", "app", appID, "deployment", ins.DeploymentID, "instance", ins.ID, "err", err)
		}
	}
	desired = e.capWorkerReplicasToAccount(ctx, app, workers, desired)

	for deploymentID := range targets {
		for kept[deploymentID] < desired {
			dep, depErr := e.store.DeploymentByID(ctx, deploymentID)
			if depErr != nil || dep.Status != state.DeployLive {
				if depErr != nil && !errors.Is(depErr, state.ErrNotFound) {
					e.log.Warn("sched: reload worker target", "app", appID, "deployment", deploymentID, "err", depErr)
				}
				break
			}
			result, admitErr := e.AdmitInstanceForDeployment(ctx, appID, deploymentID, dep.Scope, trigger)
			if admitErr != nil {
				e.log.Warn("sched: admit worker replica", "app", appID, "deployment", deploymentID, "err", admitErr)
				break
			}
			if result.AtCapacity {
				e.log.Debug("sched: worker replica admission at capacity", "app", appID, "deployment", deploymentID)
				break
			}
			kept[deploymentID]++
		}
	}
}

// stopManagedWorker removes one reconciler-owned worker without snapshots.
// RUNNING rows take the graceful OCI stop path. In-flight rows are destroyed
// under appMu so a concurrent boot cannot commit after the cleanup decision.
func (e *Engine) stopManagedWorker(ctx context.Context, instanceID string, opts ...StopOptions) error {
	for attempt := 0; attempt < 2; attempt++ {
		ins, err := e.store.InstanceByID(ctx, instanceID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil
			}
			return err
		}
		if state.State(ins.State) == state.StateRunning {
			var stopOpts StopOptions
			if len(opts) > 0 {
				stopOpts = opts[0]
			}
			_, err = e.StopInstance(ctx, instanceID, stopOpts)
			return err
		}
		if !state.State(ins.State).CountsForRAM() {
			return nil
		}

		release := e.lockApp(ins.AppID)
		fresh, reloadErr := e.store.InstanceByID(ctx, instanceID)
		if reloadErr != nil {
			release()
			if errors.Is(reloadErr, state.ErrNotFound) {
				return nil
			}
			return reloadErr
		}
		if fresh.State != ins.State {
			release()
			continue
		}
		destroyErr := e.timedDestroy(ctx, fresh.NodeID, fresh.ID, DestroyTimeout)
		if destroyErr == nil {
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
		}
		release()
		return destroyErr
	}
	return nil
}

func (e *Engine) isMirrorDeployment(ctx context.Context, appID, deploymentID string) (bool, error) {
	rules, err := e.store.ListMirrorRules(ctx, appID)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if rule.MirrorDeploymentID == deploymentID {
			return true, nil
		}
	}
	return false, nil
}

// drainIncompatibleServiceReplicas removes request/worker/job instances that
// were already live when the app switched into service mode. They cannot count
// toward the service target and must release their resident admission before a
// service replica can be admitted. In-flight rows are destroyed as well: if a
// request-mode wake were allowed to finish after the mode switch, it could
// consume capacity as an extra RUNNING VM and never trigger a service
// reconciliation because its instance mode is not service.
func (e *Engine) drainIncompatibleServiceReplicas(ctx context.Context, instances []state.Instance) int {
	removed := 0
	for _, ins := range instances {
		if normalizedInstanceMode(ins.Mode) == string(state.InstanceModeService) ||
			ins.Mode == string(state.InstanceModeMirror) {
			continue
		}
		fresh, err := e.store.InstanceByID(ctx, ins.ID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				e.log.Warn("sched: reload incompatible service instance", "instance", ins.ID, "deployment", ins.DeploymentID, "err", err)
			}
			continue
		}
		switch state.State(fresh.State) {
		case state.StateRunning:
			if err := e.Park(ctx, fresh.ID); err != nil {
				e.log.Warn("sched: park incompatible service instance", "instance", fresh.ID, "deployment", fresh.DeploymentID, "err", err)
				continue
			}
			removed++
		case state.StateWaking, state.StateColdBooting:
			// A mode switch is an explicit lifecycle change. Destroy the
			// in-flight VM and close its reservation so the subsequent
			// service admit cannot be rejected by a stale request wake.
			destroyCtx := context.WithoutCancel(ctx)
			if err := e.timedDestroy(destroyCtx, fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
				e.log.Warn("sched: destroy incompatible service wake", "instance", fresh.ID, "deployment", fresh.DeploymentID, "err", err)
				continue
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
			removed++
		}
	}
	return removed
}

// parkSurplusServiceReplicas drains the oldest RUNNING service instances first.
// ListInstancesForApp returns newest rows first, so walking backwards preserves
// the freshest replicas and avoids taking every replacement from the same boot
// generation during a scale-down.
func (e *Engine) parkSurplusServiceReplicas(ctx context.Context, replicas []state.Instance, excess int) int {
	parked := 0
	for i := len(replicas) - 1; i >= 0 && parked < excess; i-- {
		ins := replicas[i]
		if state.State(ins.State) != state.StateRunning {
			continue
		}
		if err := e.Park(ctx, ins.ID); err != nil {
			e.log.Warn("sched: park surplus service replica", "instance", ins.ID, "deployment", ins.DeploymentID, "err", err)
			continue
		}
		parked++
	}
	return parked
}
