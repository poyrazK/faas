package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	errServiceCapacity = errors.New("service admission is at capacity")
	errServiceStartup  = errors.New("service startup failed")
)

// ReconcileServiceApp retains the existing allocation/handoff controller but
// fences attempts and persists cooldown before another event can retry it.
func (e *Engine) ReconcileServiceApp(ctx context.Context, appID string) {
	ctx, cancel := context.WithTimeout(detachedServiceContext(ctx), time.Duration(api.ServiceRecoveryAttemptTimeoutSeconds)*time.Second)
	defer cancel()
	mu := e.serviceAppMutex(appID)
	mu.Lock()
	defer mu.Unlock()
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	app, err := e.store.AppByID(readCtx, appID)
	if err != nil || !e.ownsApp(app) || app.Status != state.AppActive {
		return
	}
	if instanceModeForApp(app) != string(state.InstanceModeService) {
		// Mode changes still drain old service replicas through the same controller.
		_ = e.reconcileServiceAppOnce(ctx, appID)
		return
	}
	account, err := e.store.AccountByID(readCtx, app.AccountID)
	if err != nil || !account.Active() {
		return
	}
	deps, err := e.store.LiveDeployments(readCtx, appID)
	if err != nil {
		e.log.Warn("service recovery: read desired generations", "app", appID, "err", err)
		return
	}
	now := e.now().UTC()
	token := uuid.NewString()
	lease, claimed, err := e.store.ClaimServiceRecovery(readCtx, appID, serviceRecoveryRevision(app, deps), token,
		now, now.Add(time.Duration(api.ServiceRecoveryAttemptTimeoutSeconds)*time.Second))
	if err != nil {
		e.log.Warn("service recovery: claim", "app", appID, "err", err)
		return
	}
	if !claimed {
		return
	}
	readCancel()
	attemptErr := e.reconcileServiceAppOnce(ctx, appID)
	status, readErr := e.serviceRecoveryStatus(ctx, appID)
	if readErr != nil {
		status = "waiting_dependency"
	} else if status != "ready" && attemptErr != nil {
		switch {
		case errors.Is(attemptErr, errServiceCapacity):
			status = "waiting_capacity"
		case errors.Is(attemptErr, errServiceStartup):
			status = "retrying_startup"
		default:
			status = "waiting_dependency"
		}
	}
	lease.Status = status
	lease.UpdatedAt = e.now().UTC()
	delay := time.Duration(api.ServiceRecoveryPollIntervalSeconds) * time.Second
	switch status {
	case "ready":
		lease.Failures = 0
		delay = time.Duration(api.ServiceRecoveryHealthyIntervalSeconds) * time.Second
	case "waiting_capacity", "retrying_startup", "waiting_dependency":
		if lease.Failures < api.ServiceRecoveryFailureCountMax {
			lease.Failures++
		}
		delay = serviceRecoveryBackoff(lease.Failures)
	}
	lease.NextAttemptAt = lease.UpdatedAt.Add(delay)
	// Admission/cleanup may exhaust the attempt deadline; completing the retry
	// ledger is separately bounded and must not inherit that cancellation.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if err := e.store.CompleteServiceRecovery(finishCtx, token, lease); err != nil {
		e.log.Warn("service recovery: complete", "app", appID, "err", err)
	}
}

func serviceRecoveryBackoff(failures int) time.Duration {
	d := time.Duration(api.ServiceRecoveryRetryBaseSeconds) * time.Second
	max := time.Duration(api.ServiceRecoveryRetryMaxSeconds) * time.Second
	for n := 1; n < failures && d < max; n++ {
		d *= 2
	}
	if d > max {
		return max
	}
	return d
}

// Only desired configuration/generations reset cooldown. Failed replica IDs
// are deliberately excluded so each failed boot cannot bypass its own retry.
func serviceRecoveryRevision(app state.App, deps []state.Deployment) string {
	type generation struct {
		ID, Scope, Rollout string
		Traffic            int
	}
	gens := make([]generation, 0, len(deps))
	for _, d := range deps {
		gens = append(gens, generation{d.ID, d.Scope, d.RolloutState, d.TrafficPercent})
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i].ID < gens[j].ID })
	data, _ := json.Marshal(struct {
		Manifest              state.AppManifest
		RAM, CPU, Concurrency int
		Runtime, Owner        string
		Generations           []generation
	}{
		app.Manifest, app.RAMMB, app.CPUMillicores, app.MaxConcurrency, app.Runtime, app.NodeID, gens})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (e *Engine) serviceRecoveryStatus(ctx context.Context, appID string) (string, error) {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return "waiting_dependency", err
	}
	if app.Status != state.AppActive || instanceModeForApp(app) != string(state.InstanceModeService) {
		return "ready", nil
	}
	deps, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		return "waiting_dependency", err
	}
	if len(deps) == 0 {
		if desiredServiceReplicas(app.Manifest) == 0 {
			return "ready", nil
		}
		return "waiting_dependency", nil
	}
	if len(activeServiceRollouts(deps)) > 0 {
		return "rolling_out", nil
	}
	targets, err := e.serviceReplicaTargets(ctx, app, deps)
	if err != nil {
		return "waiting_dependency", err
	}
	if len(targets) == 0 && desiredServiceReplicas(app.Manifest) > 0 {
		return "waiting_dependency", nil
	}
	outcome := "ready"
	for _, dep := range deps {
		if _, managed := targets[dep.ID]; !managed {
			continue // Mirror generations retain their separate lifecycle.
		}
		replicas, err := listServiceReplicas(ctx, e.store, appID, dep.ID)
		if err != nil {
			return "waiting_dependency", err
		}
		actual, target := classifyServiceReplicas(replicas), targets[dep.ID]
		if actual.starting > 0 {
			outcome = "starting"
			continue
		}
		if actual.draining > 0 || actual.ready > target {
			outcome = "draining"
			continue
		}
		if actual.ready < target {
			return "waiting_capacity", nil
		}
	}
	return outcome, nil
}

func (l *Loop) dispatchServiceRecovery(ctx context.Context) {
	l.submitWork(workServiceRecoverySweep, "fleet", func() { l.runServiceRecovery(ctx) })
}

func (l *Loop) runServiceRecovery(ctx context.Context) {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return
	}
	owner := l.engine.OwnerNodeID()
	if owner == "" {
		owner = l.engine.defaultLocalNodeID
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	apps, err := l.engine.store.ListServiceRecoveryApps(listCtx, owner, l.now().UTC(), api.ServiceRecoveryBatchSize)
	if err != nil {
		l.log.Warn("service recovery: list due apps", "err", err)
		return
	}
	for _, appID := range apps {
		l.submitServiceRecovery(ctx, appID)
	}
}

func (l *Loop) submitServiceRecovery(ctx context.Context, appID string) {
	l.submitWork(workServiceRecovery, appID, func() { l.engine.ReconcileServiceApp(ctx, appID) })
}
