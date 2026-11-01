package sched

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// inFlightWakePoll is how often a wake the concurrency cap refused looks for
// the instance another producer is booting.
const inFlightWakePoll = 100 * time.Millisecond

// runningWakeResult answers a wake from a running instance of dep. It carries
// the instance's own wake_id, so a reused instance correlates with the wake
// that brought it up (gaps analysis 2026-07-23 review finding #1).
func (e *Engine) runningWakeResult(ctx context.Context, appID, tenantID string, ins state.Instance, dep state.Deployment) WakeResult {
	identity := api.PlatformIdentity{
		AppID:         appID,
		DeploymentID:  dep.ID,
		InstanceID:    ins.ID,
		NodeID:        ins.NodeID,
		CommitSHA:     dep.CommitSHA,
		DeploymentTag: dep.Tag,
		ImageDigest:   dep.ImageDigest,
		TenantID:      tenantID,
	}
	if !dep.CreatedAt.IsZero() {
		identity.DeploymentCreatedAt = dep.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if node, err := e.store.ComputeNodeByID(ctx, ins.NodeID); err == nil {
		identity.Region = stringValue(node.Region)
	}
	return WakeResult{InstanceID: ins.ID, NodeID: ins.NodeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE, WakeID: ins.WakeID,
		Port: deploymentRuntimePort(dep), DeploymentID: dep.ID, RequestCount: ins.RequestCount, Identity: identity}
}

// awaitBootingInstanceForWake serves a wake the concurrency cap refused while
// the app restarts, from the replacement the restart is booting, once it runs.
// A runtime-config refresh boots its replacement in the app's one overlap
// slot and retires the old VM when the replacement runs; a gateway wake
// arriving then was refused at once with plan_limit_concurrency instead of
// waiting a second for that replacement (PulseWatch F3: 63 such 429s on
// production-us during one `restart --fresh` at max_concurrency 1). Only a
// restart's boots are waited for: wakes of one burst beyond the cap are still
// refused while the admitted boots run (TestProperty_EngineWake_RespectsMaxConcurrency).
// Nothing is admitted here, so the cap still holds; the wait ends when the
// boot ends, the cold-boot budget passes or the caller gives up, and the
// refusal then stands.
func (e *Engine) awaitBootingInstanceForWake(ctx context.Context, tenantID, appID, deploymentID, scope string, refusal error) (WakeResult, bool) {
	var problem *api.Problem
	if !errors.As(refusal, &problem) || problem.Code != api.CodePlanLimitConcur || !e.restarting(appID) {
		return WakeResult{}, false
	}
	if deploymentID == "" {
		dep, err := e.resolveLiveDeployment(ctx, appID)
		if err != nil {
			return WakeResult{}, false
		}
		deploymentID = dep.ID
	}
	budget := time.NewTimer(e.budgetFor(state.StateColdBooting))
	defer budget.Stop()
	poll := time.NewTicker(inFlightWakePoll)
	defer poll.Stop()
	for {
		booting, err := e.instanceBootingFor(ctx, appID, deploymentID)
		if err != nil {
			return WakeResult{}, false
		}
		if ins, err := e.runningInstanceForWake(ctx, appID, deploymentID, scope); err == nil && e.wakeInstanceModeMatchesApp(ctx, appID, ins) {
			if e.checkManagedPostgresAdmission(ctx, appID) != nil {
				return WakeResult{}, false
			}
			dep, err := e.store.DeploymentByID(ctx, ins.DeploymentID)
			if err != nil {
				return WakeResult{}, false
			}
			return e.runningWakeResult(ctx, appID, tenantID, ins, dep), true
		}
		if !booting {
			return WakeResult{}, false
		}
		select {
		case <-ctx.Done():
			return WakeResult{}, false
		case <-budget.C:
			return WakeResult{}, false
		case <-poll.C:
		}
	}
}

// restarting reports whether this scheduler is restarting appID (restartApp).
func (e *Engine) restarting(appID string) bool {
	e.restartMu.Lock()
	defer e.restartMu.Unlock()
	_, ok := e.restartInFlight[appID]
	return ok
}

// instanceBootingFor reports whether an instance of deploymentID is waking or
// cold booting. A fork never serves a wake, so its boot is not waited for.
func (e *Engine) instanceBootingFor(ctx context.Context, appID, deploymentID string) (bool, error) {
	instances, err := e.liveInstancesForApp(ctx, appID)
	if err != nil {
		return false, err
	}
	for _, instance := range instances {
		if instance.DeploymentID != deploymentID || state.IsFork(instance.Mode) {
			continue
		}
		switch state.State(instance.State) {
		case state.StateWaking, state.StateColdBooting:
			return true, nil
		}
	}
	return false, nil
}
