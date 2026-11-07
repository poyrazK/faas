// adr: 20261106214916

package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// Outcomes of a refresh an on pass performs.
const (
	// SnapshotRefreshRefreshed: the app booted, a fresh snapshot was
	// captured on the node and the refresh VM parked.
	SnapshotRefreshRefreshed = "refreshed"
	// SnapshotRefreshDeferred: the node lacked the headroom a refresh needs;
	// the next pass tries again.
	SnapshotRefreshDeferred = "deferred"
	// SnapshotRefreshFailed: the refresh boot or capture failed. The stale
	// snapshot was retired before the boot, so the app's next wake
	// cold-boots, as it would have without the refresh.
	SnapshotRefreshFailed = "failed"
)

// TriggerSnapshotRefresh labels a boot the platform made to refresh a
// snapshot a release made stale (ADR-20261106214916).
const TriggerSnapshotRefresh = "platform.snapshot_refresh"

// refreshTarget is a candidate a pass found stale, with what its refresh
// boots.
type refreshTarget struct {
	app    state.App
	acct   state.Account
	limits api.Limits
	dep    state.Deployment
	snap   state.Snapshot
}

// refreshHeadroom reports whether nodeID keeps the headroom a refresh needs:
// tenant residency below SnapshotRefreshMaxResidencyPercent of its ceiling,
// as for the builder's opportunistic slot. A refresh never waits for room.
func (r *SnapshotRefresher) refreshHeadroom(nodeID string) bool {
	ceiling := r.e.ledger.CeilingForNode(nodeID)
	return ceiling > 0 && r.e.ledger.AdmissionUsedMB(nodeID)*100 < api.SnapshotRefreshMaxResidencyPercent*ceiling
}

// refresh boots t's deployment on nodeID in mode refresh, captures a fresh
// init snapshot there and parks the VM: the deploy pipeline's prime without
// its deploy authority or activation. It is admitted like a prime (RAM and
// CPU, never a serving slot) and never routed or billed.
func (r *SnapshotRefresher) refresh(ctx context.Context, nodeID string, t refreshTarget) string {
	e := r.e
	if !r.refreshHeadroom(nodeID) {
		return SnapshotRefreshDeferred
	}
	ins, placement, outcome := r.admitRefresh(ctx, nodeID, t)
	if outcome != "" {
		return outcome
	}
	completed := false
	defer func() {
		if !completed && ctx.Err() != nil {
			e.cleanupInterruptedPrime(ctx, t.app.ID, ins)
		}
	}()
	if err := r.bootRefresh(ctx, ins, placement, t); err != nil {
		r.log.Warn("snapshot refresh: boot failed", "node_id", nodeID, "app_id", t.app.ID, "deployment_id", t.dep.ID, "instance", ins.ID, "err", err)
		return SnapshotRefreshFailed
	}
	completed = true
	return SnapshotRefreshRefreshed
}

// admitRefresh admits the refresh instance on nodeID and retires the stale
// snapshot, under the app's lock. A non-empty outcome means nothing was
// booted.
func (r *SnapshotRefresher) admitRefresh(ctx context.Context, nodeID string, t refreshTarget) (state.Instance, Placement, string) {
	e := r.e
	release := e.lockApp(t.app.ID)
	defer release()
	// A wake may have started since the pass looked.
	live, err := state.ListLiveInstancesForApp(ctx, e.store, t.app.ID)
	if err != nil {
		return state.Instance{}, Placement{}, SnapshotRefreshFailed
	}
	for _, ins := range live {
		if state.IsLive(ins.State) {
			return state.Instance{}, Placement{}, SnapshotRefreshSkippedLive
		}
	}
	nodes, err := e.store.ListComputeNodes(ctx, false)
	if err != nil {
		return state.Instance{}, Placement{}, SnapshotRefreshFailed
	}
	var others []string
	for _, n := range nodes {
		if n.ID != nodeID {
			others = append(others, n.ID)
		}
	}
	cpu := e.appCPUBookingMillicores(t.app, t.limits)
	wakeID := uuid.NewString()
	ins, placement, _, err := e.createWithNodePlacement(ctx, Request{
		AppID: t.app.ID, Plan: t.acct.Plan, RAMMB: t.app.RAMMB, VCPU: e.guestVCPUs(t.app, t.limits),
		CPUMillicores: cpu, MaxConcurrency: t.app.MaxConcurrency, BookedCPUMillicores: e.appCPUBookingStamp(t.app, t.limits),
		PreferredNodeID: nodeID, ExcludeNodeIDs: others,
	}, false, func(admitCtx context.Context, p Placement) (state.Instance, error) {
		if p.NodeID != nodeID {
			return state.Instance{}, state.ErrNodeCapacity
		}
		return e.store.CreateInstanceWithMode(admitCtx, t.app.ID, t.dep.ID, string(state.StateColdBooting), t.app.RAMMB, p.NodeID, wakeID, string(state.InstanceModeRefresh))
	})
	if err != nil {
		if isCapacityProblem(err) || errors.Is(err, state.ErrNodeCapacity) {
			r.log.Info("snapshot refresh: node cannot admit the refresh now", "node_id", nodeID, "app_id", t.app.ID, "err", err)
			return state.Instance{}, Placement{}, SnapshotRefreshDeferred
		}
		r.log.Warn("snapshot refresh: admit", "node_id", nodeID, "app_id", t.app.ID, "err", err)
		return state.Instance{}, Placement{}, SnapshotRefreshFailed
	}
	if err := e.bindSnapshotRecovery(ctx, ins, ""); err != nil {
		_ = e.store.DeleteInstance(context.WithoutCancel(ctx), ins.ID)
		return state.Instance{}, Placement{}, SnapshotRefreshFailed
	}
	if err := e.checkCapturedApplicationStandardAdmission(ctx, ins.ID, t.app, t.acct, t.dep); err != nil {
		_ = e.store.DeleteInstance(context.WithoutCancel(ctx), ins.ID)
		return state.Instance{}, Placement{}, SnapshotRefreshFailed
	}
	e.emitInstanceChanged(ctx, ins.ID, t.app.ID, state.StateColdBooting, wakeID)
	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: t.app.ID, DeploymentID: t.dep.ID, DeploymentScope: t.dep.Scope, Plan: t.acct.Plan,
		RAMMB: t.app.RAMMB, VCPU: e.guestVCPUs(t.app, t.limits), CPUMillicores: cpu, MaxConcurrency: t.app.MaxConcurrency,
		Kind:   KindSnapshotPrime,
		NodeID: placement.NodeID, NodeCeilingMB: placement.CeilingMB,
		VCPUBudget: placement.VCPUBudget, CPUBudgetMillicores: placement.CPUBudgetMillicores,
	}); err != nil {
		e.transitionWithKind(ctx, ins.ID, t.app.ID, state.StateFailed, "wake_boot_error", "snapshot_refresh_admit_denied")
		return state.Instance{}, Placement{}, SnapshotRefreshDeferred
	}
	// imaged keeps a deployment's live snapshot over a newer capture, so the
	// stale one is retired once the refresh is admitted, as a wake whose
	// restore fell back retires it. A refresh that is deferred keeps it, so
	// the next pass finds it again.
	if err := e.store.MarkSnapshotStale(ctx, t.snap.ID); err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, t.app.ID, "snapshot_refresh_retire_failed")
		return state.Instance{}, Placement{}, SnapshotRefreshFailed
	}
	return ins, placement, ""
}

// bootRefresh cold-boots the admitted refresh instance, publishes it and
// captures and parks it. Every failure releases the instance.
func (r *SnapshotRefresher) bootRefresh(ctx context.Context, ins state.Instance, placement Placement, t refreshTarget) error {
	e := r.e
	appID := t.app.ID
	prepared, err := e.prepareDeploymentPrimeBoot(ctx, t.app, t.acct, t.limits, t.dep, placement, ins)
	if err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, appID, prepared.RejectionReason)
		return err
	}
	delivery := bootInput{
		insID: ins.ID, appID: appID, accountID: t.acct.ID, wakeID: ins.WakeID, trigger: TriggerSnapshotRefresh,
		secretDeliveries: prepared.SecretDeliveries, secretFence: prepared.SecretFence, configFence: prepared.ConfigFence,
	}
	delivered := false
	defer func() {
		if !delivered {
			e.recordAppSecretDelivery(ctx, delivery, state.SecretDeliveryFailed, "runtime_start_failed")
		}
	}()
	spec := prepared.Spec
	if err := e.verifyPrimeLayer(ctx, appID, spec.LayerKey); err != nil {
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "snapshot_refresh_sig_verify")
		e.ledger.Release(ins.ID)
		return err
	}
	bootCtx, cancel := context.WithTimeout(ctx, e.primeColdBootBudget(spec.StartupDeadlineS))
	defer cancel()
	startedAt := time.Now().UTC()
	r.emitRefreshBootStarted(bootCtx, ins, placement.NodeID, startedAt)
	out, err := e.createRuntimeWithStandards(bootCtx, placement.NodeID, ins.ID, string(state.StateColdBooting), spec, nil, false)
	if err != nil {
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "snapshot_refresh_cold_boot_failed")
		return fmt.Errorf("sched: snapshot refresh: cold boot: %w", err)
	}
	if out == nil || out.Instance != ins.ID || out.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "snapshot_refresh_identity_invalid")
		return fmt.Errorf("sched: snapshot refresh: cold boot response does not prove the admitted instance: %w", state.ErrConflict)
	}
	// Publication and capture run under the app's lock, as every park does;
	// the cold boot above ran without it, so a user's wake of the app was not
	// held behind the boot.
	release := e.lockApp(appID)
	defer release()
	inputs := prepared.Inputs
	published, err := e.publishPrimeRuntime(ctx, out, state.RuntimeInstancePublication{
		AccountID: t.acct.ID, AppID: appID, InstanceID: ins.ID, NodeID: placement.NodeID, WakeID: ins.WakeID,
		ExpectedState: string(state.StateColdBooting), Netns: out.Netns, HostIP: out.HostIP, GuestUID: int(out.LeaseUID),
		Fence: prepared.SecretFence, ConfigFence: prepared.ConfigFence, Inputs: &inputs,
	})
	if err != nil {
		if errors.Is(err, state.ErrFinancialBudgetHeld) {
			return errors.Join(err, e.retireFinancialBudgetRuntime(ctx, ins))
		}
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "snapshot_refresh_record_runtime_failed")
		return fmt.Errorf("sched: snapshot refresh: record runtime: %w", applicationStandardRuntimeProblem(err))
	}
	e.recordCommittedInstanceTransition(ctx, published, state.StateColdBooting, state.StateRunning, appID, "state_transition", "")
	r.emitRefreshBootCompleted(ctx, published, placement.NodeID, startedAt)
	e.recordAppSecretDelivery(ctx, delivery, state.SecretDeliveryDelivered, "")
	delivered = true
	// A fresh capture from this boot: the stale row was retired, so no
	// usable row may be reused in its place.
	return e.snapshotAndParkPrime(ctx, published)
}

// emitRefreshBootStarted and emitRefreshBootCompleted put the refresh boot
// on the app's wake timeline with trigger platform.snapshot_refresh, so a
// boot the customer did not cause is labelled as the platform's.
func (r *SnapshotRefresher) emitRefreshBootStarted(ctx context.Context, ins state.Instance, nodeID string, at time.Time) {
	if r.e.events == nil {
		return
	}
	r.e.events.EmitAsync(ctx, events.BootStarted{
		EmitAt: at, WakeID: ins.WakeID, AppID: ins.AppID, InstanceID: ins.ID, NodeID: nodeID,
		Method: "cold_boot", Tier: wakeTierColdBootFallback, ColdReason: ColdReasonStale,
		RequestedAt: at, Trigger: TriggerSnapshotRefresh,
	})
}

func (r *SnapshotRefresher) emitRefreshBootCompleted(ctx context.Context, ins state.Instance, nodeID string, startedAt time.Time) {
	if r.e.events == nil {
		return
	}
	r.e.events.EmitAsync(ctx, events.BootCompleted{
		EmitAt: time.Now().UTC(), WakeID: ins.WakeID, AppID: ins.AppID, InstanceID: ins.ID, NodeID: nodeID,
		Method: "cold_boot", Tier: wakeTierColdBootFallback, StartedAt: startedAt, CompletedAt: time.Now().UTC(),
		Trigger: TriggerSnapshotRefresh,
	})
}

// reapOrphanedRefreshes retires refresh instances on nodeID that outlived
// SnapshotRefreshMaxRuntime: a scheduler that stopped mid-refresh leaves its
// VM running, and the reaper does not idle-reap the refresh mode.
func (r *SnapshotRefresher) reapOrphanedRefreshes(ctx context.Context, nodeID string) {
	e := r.e
	reader, ok := e.store.(state.ResidentInstancesOnNodeReader)
	if !ok {
		return
	}
	resident, err := reader.ListResidentInstancesOnNode(ctx, nodeID)
	if err != nil {
		r.log.Warn("snapshot refresh: list resident instances", "node_id", nodeID, "err", err)
		return
	}
	cutoff := r.now().Add(-api.SnapshotRefreshMaxRuntime)
	for _, ins := range resident {
		if !state.IsRefresh(ins.Mode) || ins.StartedAt.After(cutoff) {
			continue
		}
		if err := e.timedDestroy(ctx, nodeID, ins.ID, DestroyTimeout); err != nil {
			r.log.Warn("snapshot refresh: destroy orphaned refresh", "node_id", nodeID, "instance", ins.ID, "err", err)
			continue
		}
		e.rollbackAdmittedInstance(ctx, ins.ID, ins.AppID, "snapshot_refresh_orphaned")
		r.log.Info("snapshot refresh: retired an orphaned refresh instance", "node_id", nodeID, "instance", ins.ID, "app_id", ins.AppID,
			"started_at", ins.StartedAt.Format(time.RFC3339))
	}
}
