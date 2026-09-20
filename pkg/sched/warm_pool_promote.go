package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// promoteWarmInstanceLocked resumes one resident warm-pool VM for a request.
// The caller holds appMu; keeping the resume and the WARM -> RUNNING CAS in
// that window prevents two local wake callers from claiming the same row.
func (e *Engine) promoteWarmInstanceLocked(ctx context.Context, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, mode string) (WakeResult, bool, error) {
	if app.Status != state.AppActive || app.WarmPoolSize <= 0 || !instanceModeUsesSnapshots(mode) || !api.Plan(acct.Plan).WarmPoolAllowed() {
		return WakeResult{}, false, nil
	}
	if e.ledger.Concurrency(app.ID) >= effectiveMaxConcurrency(app, limits) {
		// The existing admission gate owns the at-capacity result; do not
		// consume a warm row or emit a misleading "missing" outcome.
		return WakeResult{}, false, nil
	}
	instances, err := e.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		return WakeResult{}, false, fmt.Errorf("sched: warm pool: list promotion candidates: %w", err)
	}
	candidates := make([]state.Instance, 0, len(instances))
	warmCount := 0
	for _, ins := range instances {
		if state.State(ins.State) != state.StateWarm {
			continue
		}
		warmCount++
		if ins.DeploymentID != dep.ID || !instanceModeMatchesApp(app, ins) {
			continue
		}
		candidates = append(candidates, ins)
	}
	defer func() { e.setWarmPoolSizeGauge(acct.Plan, warmCount) }()
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].StartedAt.Equal(candidates[j].StartedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].StartedAt.After(candidates[j].StartedAt)
	})
	if len(candidates) == 0 {
		e.observeWarmResume("missing")
		return WakeResult{}, false, nil
	}

	resumer, ok := e.vmm.(WarmResumeVMM)
	if !ok {
		// Older vmmd nodes may still own a warm row during a rolling upgrade.
		// Retire those paused leases so the ordinary wake path can make progress.
		for _, warm := range candidates {
			e.discardWarmPromotion(ctx, warm, "resume_capability_unavailable")
			e.observeWarmResume("stale")
		}
		return WakeResult{}, false, nil
	}

	for _, warm := range candidates {
		// A paused row without runtime identity cannot be resumed safely;
		// destroy and park it so it cannot consume resident capacity forever.
		if warm.Netns == "" || warm.HostIP == "" {
			if e.discardWarmPromotion(ctx, warm, "runtime_identity_missing") {
				warmCount--
			}
			e.observeWarmResume("stale")
			continue
		}
		if !e.ledger.ResidentFor(warm.ID) {
			ceiling, vcpuBudget, ceilingErr := e.resolveNodeCeiling(ctx, warm.NodeID)
			cpuBudgetMillicores := e.resolveNodeCPUBudgetMillicores(ctx, warm.NodeID)
			if ceilingErr != nil {
				e.log.Warn("sched: warm pool: repair missing ledger reservation", "instance", warm.ID, "err", ceilingErr)
			}
			if admitErr := e.ledger.Admit(Request{
				Instance: warm.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: acct.Plan,
				RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency,
				NodeID: warm.NodeID, NodeCeilingMB: ceiling, VCPUBudget: vcpuBudget, CPUBudgetMillicores: cpuBudgetMillicores, Kind: KindWarmPool,
			}); admitErr != nil {
				if e.discardWarmPromotion(ctx, warm, "ledger_repair_failed") {
					warmCount--
				}
				e.observeWarmResume("stale")
				continue
			}
		}

		resumeStartedAt := time.Now()
		resumeCtx, cancel := context.WithTimeout(ctx, e.budgetForWake(bootInput{haveSnap: true, snapKey: "warm_pool"}))
		resumeErr := resumer.ResumeWarmInstance(resumeCtx, e.nodeForRoute(warm.NodeID), warm.ID)
		cancel()
		if resumeErr != nil {
			if e.discardWarmPromotion(ctx, warm, "resume_failed") {
				warmCount--
			}
			e.observeWarmResume("stale")
			continue
		}
		if !e.ledger.PromoteWarm(warm.ID) {
			if e.discardWarmPromotion(ctx, warm, "ledger_promotion_failed") {
				warmCount--
			}
			e.observeWarmResume("stale")
			continue
		}

		fresh, publishErr := e.store.PublishInstanceRuntime(ctx, warm.ID, string(state.StateWarm), warm.Netns, warm.HostIP, warm.GuestUID)
		if publishErr != nil {
			e.ledger.Release(warm.ID)
			if !errors.Is(publishErr, state.ErrConflict) {
				if e.discardWarmPromotion(ctx, warm, "record_runtime_failed") {
					warmCount--
				}
			} else if current, loadErr := e.store.InstanceByID(ctx, warm.ID); loadErr == nil && state.State(current.State) == state.StateWarm {
				if e.discardWarmPromotion(ctx, warm, "state_conflict") {
					warmCount--
				}
			}
			e.observeWarmResume("stale")
			continue
		}

		e.recordCommittedInstanceTransition(ctx, fresh, state.StateWarm, state.StateRunning, app.ID, "warm_pool_resume", "")
		e.clearSnapshotBackoffAfterWake(ctx, dep.ID)
		e.observeWarmResumeDuration(app.ID, warm.WakeID, time.Since(resumeStartedAt))
		warmCount--
		e.observeWarmResume("success")
		region := ""
		if node, nodeErr := e.store.ComputeNodeByID(ctx, fresh.NodeID); nodeErr == nil {
			region = stringValue(node.Region)
		}
		return WakeResult{
			InstanceID:   fresh.ID,
			NodeID:       fresh.NodeID,
			Method:       vmmdpb.WakeMethod_WAKE_RESTORE,
			WakeID:       fresh.WakeID,
			Port:         deploymentRuntimePort(dep),
			DeploymentID: dep.ID,
			RequestCount: fresh.RequestCount,
			Identity:     platformIdentity(app, dep, acct, fresh.NodeID, fresh.ID, region),
		}, true, nil
	}
	return WakeResult{}, false, nil
}

func (e *Engine) observeWarmResume(outcome string) {
	if e != nil && e.ops != nil {
		e.ops.WarmPoolResumeTotal(outcome).Inc()
	}
}

// discardWarmPromotion destroys a paused lease and moves its row back to
// PARKED. The conditional state write prevents a stale cleanup from parking a
// row that another scheduler transition has already claimed.
func (e *Engine) discardWarmPromotion(ctx context.Context, warm state.Instance, reason string) bool {
	cleanupCtx := context.WithoutCancel(ctx)
	if err := e.timedDestroy(cleanupCtx, warm.NodeID, warm.ID, DestroyTimeout); err != nil {
		e.log.Warn("sched: warm pool: destroy stale promotion candidate", "instance", warm.ID, "reason", reason, "err", err)
	}
	e.ledger.Release(warm.ID)
	if err := updateInstanceStateCAS(cleanupCtx, e.store, warm.ID, string(state.StateWarm), string(state.StateParked)); err != nil {
		if !errors.Is(err, state.ErrConflict) {
			e.log.Warn("sched: warm pool: park stale promotion candidate", "instance", warm.ID, "reason", reason, "err", err)
		}
		return false
	}
	e.recordCommittedInstanceTransition(cleanupCtx, warm, state.StateWarm, state.StateParked, warm.AppID, "warm_pool_resume_failed", reason)
	return true
}

func (e *Engine) observeWarmResumeDuration(appID, wakeID string, duration time.Duration) {
	if e == nil || e.ops == nil {
		return
	}
	obs, ok := e.ops.WakeRPCDuration(appID, "resume").(prometheus.ExemplarObserver)
	if !ok {
		return
	}
	exemplar := prometheus.Labels{}
	if wakeID != "" {
		exemplar["wake_id"] = wakeID
	}
	obs.ObserveWithExemplar(duration.Seconds(), exemplar)
}
