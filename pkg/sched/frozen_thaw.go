package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/state"
)

// promoteFrozenInstanceLocked thaws one frozen VM of the deployment for a
// request (ADR-1151). The caller holds the app lock, so two wakes cannot
// claim the same row. It re-admits the full reservation like a wake; a VM
// that cannot be re-admitted, fails to thaw or no longer matches the app's
// config is parked and the request takes the ordinary wake path. It runs
// whether or not the frozen tier is enabled, so turning it off drains the
// tier through requests as well as the TTL.
//
// wakeStarted is when the shared wake path began; the thaw's
// wake.thaw_breakdown event reports the time spent before and inside it.
func (e *Engine) promoteFrozenInstanceLocked(ctx context.Context, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, mode string, wakeStarted time.Time) (WakeResult, bool, error) {
	timing := thawTiming{wakeStarted: wakeStarted, thawTried: time.Now()}
	// The ledger knows which apps have frozen VMs, so an app without one
	// costs no instance listing on the wake path.
	if app.Status != state.AppActive || !instanceModeUsesSnapshots(mode) || e.ledger.FrozenCountForApp(app.ID) == 0 {
		return WakeResult{}, false, nil
	}
	// The candidates (instance list, then their nodes) and the runtime values
	// do not depend on each other; reading them at once saves the values'
	// round trip. The ledger counted a frozen VM, so values are rarely wasted.
	var (
		candidates   []frozenCandidate
		candidateErr error
		values       runtimeDeploymentValues
		valuesErr    error
		ownerErr     error
	)
	concurrently(
		func() { candidates, candidateErr = e.frozenCandidates(ctx, app, dep) },
		func() { values, valuesErr = e.loadRuntimeDeploymentValues(ctx, app, dep) },
		func() { ownerErr = e.ownerGenerationHolds(ctx) },
	)
	if candidateErr != nil || len(candidates) == 0 {
		return WakeResult{}, false, candidateErr
	}
	if ownerErr != nil {
		return WakeResult{}, false, fmt.Errorf("sched: frozen thaw: %w", ownerErr)
	}
	if valuesErr != nil {
		return WakeResult{}, false, fmt.Errorf("sched: frozen thaw: runtime values: %w", valuesErr)
	}
	environmentKey := runtimeEnvironmentAdmissionKey(values.Snapshot.Scope, values.Snapshot.EnvironmentID)
	production := reaperProductionScope(values.Snapshot.Scope)
	if _, _, refused := e.wakeServingCapacity(app, limits, dep.ID, environmentKey, production).refusal(); refused {
		// The ordinary admission gate owns the capacity result.
		return WakeResult{}, false, nil
	}
	thawer, ok := e.vmm.(FrozenVMM)
	if !ok || runtimeValuesHaveEphemeralSecrets(values.Snapshot) {
		for _, candidate := range candidates {
			e.parkFrozenLocked(ctx, candidate.ins, "ineligible")
		}
		return WakeResult{}, false, nil
	}
	for _, candidate := range candidates {
		fresh, err := e.snapshotRecoveryFresh(ctx, candidate.ins.ID)
		if err != nil {
			return WakeResult{}, false, fmt.Errorf("sched: frozen thaw: snapshot recovery lineage: %w", err)
		}
		if !fresh {
			e.parkFrozenLocked(ctx, candidate.ins, "operator_cold_boot")
			continue
		}
		result, thawed, err := e.thawFrozenLocked(ctx, thawer, app, acct, limits, dep, values, candidate, timing)
		if err != nil {
			return WakeResult{}, false, err
		}
		if thawed {
			return result, true, nil
		}
	}
	return WakeResult{}, false, nil
}

// unconfirmedThawWork fails a thaw that did not confirm the work it was asked
// to run. A vmmd that predates a request field answers without it, and the
// instance must not serve without its healthcheck or its after_restore.
func unconfirmedThawWork(req ThawRequest, result ThawResult) error {
	switch {
	case req.ImageHealthcheckRequired && !result.HealthcheckVerified:
		return errors.New("thaw did not verify the required image healthcheck")
	case req.RunAfterRestore && !result.AfterRestoreRan:
		return errors.New("thaw did not run the app's after_restore hook")
	}
	return nil
}

// frozenCandidate is a frozen row with its node, read once for the whole
// thaw: reachability, re-admission limits and the answer's region all use
// it. haveNode is false when the read failed for another reason than a
// missing node; the thaw then falls back as each of those reads did.
// nodeMissing means the node has no row, so it is unreachable.
type frozenCandidate struct {
	ins         state.Instance
	node        state.ComputeNode
	haveNode    bool
	nodeMissing bool
}

// frozenCandidates lists the deployment's frozen rows, newest first, with
// the wake's preferred instance (WithPreferredInstance) ahead of the rest.
// Rows whose shape no longer matches the app, or whose node is gone, stale
// or leaving (draining, maintenance), are parked. Each node is read once however many candidates it holds.
func (e *Engine) frozenCandidates(ctx context.Context, app state.App, dep state.Deployment) ([]frozenCandidate, error) {
	instances, err := e.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		return nil, fmt.Errorf("sched: frozen thaw: list candidates: %w", err)
	}
	nodes := map[string]frozenCandidate{}
	var out []frozenCandidate
	for _, ins := range instances {
		if state.State(ins.State) != state.StateFrozen || ins.DeploymentID != dep.ID {
			continue
		}
		if ins.RAMMB != app.RAMMB || !instanceModeMatchesApp(app, ins) || ins.Netns == "" || ins.HostIP == "" {
			e.parkFrozenLocked(ctx, ins, "stale")
			continue
		}
		candidate, seen := nodes[ins.NodeID]
		if !seen {
			candidate = e.frozenCandidateNode(ctx, ins.NodeID)
			nodes[ins.NodeID] = candidate
		}
		if candidate.nodeMissing || candidate.haveNode && e.frozenNodeRowUnreachable(candidate.node) {
			e.parkFrozenLocked(ctx, ins, "node_restart")
			continue
		}
		// A node that no longer admits work must not take a VM back into
		// service: a thaw there keeps the drain waiting on it
		// (ADR-20261024085817). The wake then places a fresh instance.
		if candidate.haveNode && nodeLeaving(candidate.node) {
			e.parkFrozenLocked(ctx, ins, parkReasonNodeDraining)
			continue
		}
		candidate.ins = ins
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ins.LastRequestAt.Equal(out[j].ins.LastRequestAt) {
			return out[i].ins.ID < out[j].ins.ID
		}
		return out[i].ins.LastRequestAt.After(out[j].ins.LastRequestAt)
	})
	// A session pinned to one of these VMs thaws its own first.
	if preferred := PreferredInstanceFromContext(ctx); preferred != "" {
		for i, candidate := range out {
			if candidate.ins.ID == preferred {
				copy(out[1:i+1], out[:i])
				out[0] = candidate
				break
			}
		}
	}
	return out, nil
}

// frozenCandidateNode reads a candidate's node once.
func (e *Engine) frozenCandidateNode(ctx context.Context, nodeID string) frozenCandidate {
	if nodeID == "" {
		return frozenCandidate{}
	}
	node, err := e.store.ComputeNodeByID(ctx, nodeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return frozenCandidate{nodeMissing: true}
	case err != nil:
		e.log.Warn("sched: frozen thaw: node lookup", "node", nodeID, "err", err)
		return frozenCandidate{}
	}
	return frozenCandidate{node: node, haveNode: true}
}

// frozenThawPlan is what thawing one frozen VM needs: from the ordinary
// path's reads (ADR-1151) or from a ticket recorded at freeze (ADR-1740).
// app is resolved for the deployment; node holds the VM's node admission
// limits, zero for a node that could not be read (global defaults).
type frozenThawPlan struct {
	frozen         state.Instance
	app            state.App
	acct           state.Account
	limits         api.Limits
	dep            state.Deployment
	environmentKey string
	production     bool
	node           nodeAdmissionLimits
	region         string
}

// thawFrozenLocked thaws one candidate. thawed=false means the candidate was
// parked and the caller may try the next one.
func (e *Engine) thawFrozenLocked(ctx context.Context, thawer FrozenVMM, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, values runtimeDeploymentValues, candidate frozenCandidate, timing thawTiming) (WakeResult, bool, error) {
	frozen := candidate.ins
	captured, proofErr := e.store.InstanceRuntimeConfigFence(ctx, acct.ID, app.ID, frozen.ID)
	if proofErr != nil && !errors.Is(proofErr, state.ErrNotFound) {
		return WakeResult{}, false, fmt.Errorf("sched: frozen thaw: captured config: %w", proofErr)
	}
	if proofErr != nil || captured != values.ConfigFence {
		e.parkFrozenLocked(ctx, frozen, "config_changed")
		return WakeResult{}, false, nil //nolint:nilerr // a VM without its captured config proof must not thaw; it parks and the ordinary wake proceeds
	}
	plan := frozenThawPlan{frozen: frozen, app: app, acct: acct, limits: limits, dep: dep,
		environmentKey: runtimeEnvironmentAdmissionKey(values.Snapshot.Scope, values.Snapshot.EnvironmentID),
		production:     reaperProductionScope(values.Snapshot.Scope)}
	if candidate.haveNode {
		plan.node = nodeAdmissionLimits{ceilingMB: candidate.node.AdmissionCeilingMB, vcpuBudget: candidate.node.VCPUBudget,
			cpuBudgetMillicores: int(cpuBudgetMillicores(candidate.node))}
		plan.region = stringValue(candidate.node.Region)
	}
	if !e.readmitFrozenLocked(ctx, plan) || !e.thawFrozenVM(ctx, thawer, plan, &timing) {
		return WakeResult{}, false, nil
	}
	bookkeeping := e.publicationBookkeeping(frozen.ID, app.ID, frozen.WakeID, state.StateFrozen, state.StateRunning, "frozen_thaw")
	fresh, publishErr := e.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: acct.ID, AppID: app.ID, InstanceID: frozen.ID, NodeID: frozen.NodeID, WakeID: frozen.WakeID,
		ExpectedState: string(state.StateFrozen), Fence: captured.SecretFence, ConfigFence: captured,
		Netns: frozen.Netns, HostIP: frozen.HostIP, GuestUID: frozen.GuestUID, Bookkeeping: bookkeeping,
	})
	timing.published = time.Now()
	if publishErr != nil {
		e.log.Warn("sched: frozen thaw: publish RUNNING", "instance", frozen.ID, "err", publishErr)
		e.abandonThawPublication(ctx, frozen)
		return WakeResult{}, false, nil
	}
	return e.answerThaw(ctx, plan, fresh, bookkeeping, timing, "success"), true, nil
}

// readmitFrozenLocked re-admits the frozen VM's full reservation on its
// node, as a wake would. A refused re-admission parks the VM; the ordinary
// wake path then owns the capacity answer.
func (e *Engine) readmitFrozenLocked(ctx context.Context, plan frozenThawPlan) bool {
	frozen := plan.frozen
	e.ledger.Release(frozen.ID)
	if err := e.ledger.Admit(Request{
		Instance: frozen.ID, AppID: plan.app.ID, DeploymentID: plan.dep.ID, DeploymentScope: plan.dep.Scope, Plan: plan.acct.Plan,
		EnvironmentKey: plan.environmentKey, ProductionEnvironment: plan.production,
		RAMMB: plan.app.RAMMB, VCPU: plan.limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(plan.app), MaxConcurrency: plan.app.MaxConcurrency,
		NodeID: frozen.NodeID, NodeCeilingMB: plan.node.ceilingMB, VCPUBudget: plan.node.vcpuBudget,
		CPUBudgetMillicores: plan.node.cpuBudgetMillicores,
	}); err != nil {
		e.log.Debug("sched: frozen thaw: re-admission refused; parking", "instance", frozen.ID, "err", err)
		e.parkFrozenLocked(ctx, frozen, "no_capacity")
		e.observeThaw("no_capacity")
		return false
	}
	return true
}

// thawFrozenVM resumes the VM through vmmd. A failed thaw parks it.
func (e *Engine) thawFrozenVM(ctx context.Context, thawer FrozenVMM, plan frozenThawPlan, timing *thawTiming) bool {
	frozen := plan.frozen
	timing.rpcStarted = time.Now()
	thawCtx, cancel := context.WithTimeout(ctx, frozenRPCTimeout)
	// Declared callbacks and qualified automatic resources reconnect their
	// clients after the time spent frozen, as they do after a restore.
	req := ThawRequest{ImageHealthcheckRequired: imageHealthcheckRequiredFromDep(plan.dep), RunAfterRestore: plan.app.Manifest.AfterRestore != nil || plan.app.Type == state.AppTypeFunction && functionlifetime.NativeNode(plan.app.Runtime)}
	result, thawErr := thawer.ThawInstance(thawCtx, e.nodeForRoute(frozen.NodeID), frozen.ID, req)
	cancel()
	timing.rpcEnded = time.Now()
	if thawErr == nil {
		thawErr = unconfirmedThawWork(req, result)
	}
	if thawErr != nil {
		e.log.Warn("sched: frozen thaw failed; parking", "instance", frozen.ID, "err", thawErr)
		e.parkFrozenLocked(ctx, frozen, "thaw_failed")
		e.observeThaw("failed")
		return false
	}
	return true
}

// abandonThawPublication cleans up after a thawed VM's publication failed:
// a row still FROZEN parks with its VM; otherwise the reservation goes and
// the wake path reads the row's actual state.
func (e *Engine) abandonThawPublication(ctx context.Context, frozen state.Instance) {
	if current, loadErr := e.store.InstanceByID(ctx, frozen.ID); loadErr == nil && state.State(current.State) == state.StateFrozen {
		e.parkFrozenLocked(ctx, frozen, "publish_failed")
	} else {
		e.ledger.Release(frozen.ID)
	}
	e.observeThaw("failed")
}

// answerThaw finishes a published thaw and answers the gateway, as a
// restore, as a warm-pool resume does.
func (e *Engine) answerThaw(ctx context.Context, plan frozenThawPlan, fresh state.Instance, bookkeeping *state.RuntimePublicationBookkeeping, timing thawTiming, outcome string) WakeResult {
	e.thawTickets.drop(fresh.ID)
	e.recordPublishedInstanceTransition(ctx, fresh, state.StateRunning, plan.app.ID, plan.dep.ID, bookkeeping)
	e.observeThaw(outcome)
	e.emitThawBreakdown(ctx, plan.app.ID, fresh, timing, time.Now())
	return WakeResult{
		InstanceID: fresh.ID, NodeID: fresh.NodeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE,
		WakeID: fresh.WakeID, Port: deploymentRuntimePort(plan.dep), DeploymentID: plan.dep.ID,
		RequestCount: fresh.RequestCount, Identity: platformIdentity(plan.app, plan.dep, plan.acct, fresh.NodeID, fresh.ID, plan.region),
	}
}

// thawTiming holds one thaw's phase boundaries for wake.thaw_breakdown.
type thawTiming struct {
	wakeStarted, thawTried, rpcStarted, rpcEnded, published time.Time
}

// emitThawBreakdown records where one successful thaw spent its time. A
// thaw keeps the instance's wake_id (the incarnation the exclusive-work
// and config-proof checks match), so this event and the gateway's
// wake.proxy_first_byte for the same thaw join on wake_id and time.
func (e *Engine) emitThawBreakdown(ctx context.Context, appID string, ins state.Instance, timing thawTiming, answered time.Time) {
	e.log.Debug("sched: thawed frozen instance", "instance", ins.ID, "duration", answered.Sub(timing.rpcStarted))
	if e.events == nil {
		return
	}
	ms := func(from, to time.Time) int64 {
		if from.IsZero() || to.Before(from) {
			return 0
		}
		return to.Sub(from).Milliseconds()
	}
	e.events.EmitAsync(ctx, events.ThawBreakdown{
		EmitAt: answered.UTC(), WakeID: ins.WakeID, AppID: appID, InstanceID: ins.ID, NodeID: ins.NodeID,
		WakeReadsMs:    ms(timing.wakeStarted, timing.thawTried),
		ThawReadsMs:    ms(timing.thawTried, timing.rpcStarted),
		VmmdThawMs:     ms(timing.rpcStarted, timing.rpcEnded),
		PublishMs:      ms(timing.rpcEnded, timing.published),
		AfterPublishMs: ms(timing.published, answered),
		TotalMs:        ms(timing.wakeStarted, answered),
	})
}
