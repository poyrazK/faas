package sched

// Frozen tier (ADR-1151 decision 2). At its idle timeout an eligible
// instance is frozen in place instead of parked: vmmd pauses the VM and
// keeps it resident, the row moves RUNNING -> FROZEN, and the ledger books
// only the instance's measured memory. The next request thaws it through
// promoteFrozenInstanceLocked, which re-admits the full reservation like a
// wake. FrozenTTL, memory pressure, a superseded deployment, a changed
// config and a failed thaw park it: the VM is destroyed and the deployment's
// snapshot stays the source of truth (ADR-005).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// frozenRPCTimeout bounds one vmmd freeze or thaw call.
const frozenRPCTimeout = 10 * time.Second

// EnableFrozenTier turns on freezing at the idle timeout (schedd
// `frozen_tier`, off by default). Thawing frozen rows and parking them stay
// active either way, so turning the switch off drains the tier.
func (e *Engine) EnableFrozenTier() { e.frozenTier.Store(true) }

func (e *Engine) frozenTierEnabled() bool { return e.frozenTier.Load() }

// EnableFrozenTierApps extends the frozen tier to request-mode apps (schedd
// `frozen_tier_apps`, off by default). It has no effect unless the frozen
// tier itself is enabled.
func (e *Engine) EnableFrozenTierApps() { e.frozenTierApps.Store(true) }

// EnableEarlyFreeze freezes quiet between_requests instances after quiet
// instead of at their idle timeout (schedd `frozen_tier_early_freeze_after_s`,
// ADR-20261021215353). Frozen time is not billed, so such an instance stops
// accruing usage shortly after its last request. It has no effect unless the
// frozen tier is enabled.
func (e *Engine) EnableEarlyFreeze(quiet time.Duration) {
	e.earlyFreezeAfterMS.Store(quiet.Milliseconds())
}

func (e *Engine) earlyFreezeAfter() time.Duration {
	if !e.frozenTierEnabled() {
		return 0
	}
	return time.Duration(e.earlyFreezeAfterMS.Load()) * time.Millisecond
}

// frozenTierAllows reports whether the frozen tier may freeze an idle
// instance of app (ADR-1151): a request-serving workload whose declared
// pause is not never. Apps other than functions also need the apps switch.
func (e *Engine) frozenTierAllows(app state.App, mode, privateNetworkID string) bool {
	if !pauseTierWorkload(app, mode, privateNetworkID) {
		return false
	}
	function := app.Type == state.AppTypeFunction
	if api.EffectivePause(app.Manifest.Pause, function) == api.PauseNever {
		return false
	}
	return function || e.frozenTierApps.Load()
}

// FreezeIdle freezes an idle RUNNING instance in place of parking it. It
// reports handled=false, with no error, when the instance is not eligible;
// the reaper then parks it as before. residentMB is the instance's latest
// measured memory, 0 when unmeasured.
func (e *Engine) FreezeIdle(ctx context.Context, instanceID string, residentMB int) (handled bool, err error) {
	return e.freeze(ctx, instanceID, residentMB, false)
}

// FreezeEarly freezes a quiet RUNNING instance before its idle timeout
// (ADR-20261021215353). Only instances the frozen tier would freeze at the
// idle timeout and whose declared pause is between_requests qualify; anything
// else reports handled=false and stays running for the idle reaper.
func (e *Engine) FreezeEarly(ctx context.Context, instanceID string, residentMB int) (handled bool, err error) {
	if e.earlyFreezeAfter() <= 0 {
		return false, nil
	}
	return e.freeze(ctx, instanceID, residentMB, true)
}

func (e *Engine) freeze(ctx context.Context, instanceID string, residentMB int, early bool) (handled bool, err error) {
	if !e.frozenTierEnabled() {
		return false, nil
	}
	freezer, ok := e.vmm.(FrozenVMM)
	if !ok {
		return false, nil
	}
	ins, err := e.lockedRunning(ctx, instanceID)
	if err != nil || ins == nil {
		return false, err
	}
	defer e.unlockApp(ins.AppID)
	if reason, err := e.freezeRefusal(ctx, *ins, early); err != nil || reason != "" {
		if reason != "" {
			e.observeFreeze(reason)
		}
		return false, err
	}
	// Leave the gateway's routes first. From here a request takes schedd's
	// wake path, which waits on this app lock and then thaws the VM.
	if err := updateInstanceStateCAS(ctx, e.store, ins.ID, string(state.StateRunning), string(state.StateFrozen)); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return false, nil
		}
		return false, fmt.Errorf("sched: freeze %s: %w", ins.ID, err)
	}
	e.recordCommittedInstanceTransition(ctx, *ins, state.StateRunning, state.StateFrozen, ins.AppID, "idle_freeze", "")
	freezeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), frozenRPCTimeout)
	_, freezeErr := freezer.FreezeInstance(freezeCtx, e.nodeForRoute(ins.NodeID), ins.ID, false)
	cancel()
	if freezeErr != nil {
		// The guest still runs: put the row back so the gateway routes it.
		// A busy guest (a request raced the freeze) stays RUNNING for the
		// next tick; any other failure falls back to the ordinary park.
		busy := problemCode(freezeErr) == api.CodeInstanceBusy
		if casErr := updateInstanceStateCAS(context.WithoutCancel(ctx), e.store, ins.ID, string(state.StateFrozen), string(state.StateRunning)); casErr != nil {
			e.log.Warn("sched: freeze: restore RUNNING after failed freeze", "instance", ins.ID, "err", casErr)
		} else {
			e.recordCommittedInstanceTransition(ctx, *ins, state.StateFrozen, state.StateRunning, ins.AppID, "idle_freeze_failed", problemCode(freezeErr))
		}
		if busy {
			e.observeFreeze("busy")
			return true, nil
		}
		e.observeFreeze("error")
		e.log.Warn("sched: freeze failed; parking instead", "instance", ins.ID, "err", freezeErr)
		return false, nil
	}
	charge := frozenChargeMB(ins.RAMMB, float64(residentMB))
	e.ledger.Freeze(ins.ID, charge)
	if early {
		e.observeFreeze("frozen_early")
	} else {
		e.observeFreeze("frozen")
	}
	if e.frozenFastPath.Load() {
		e.issueThawTicket(ctx, *ins, charge)
	}
	return true, nil
}
