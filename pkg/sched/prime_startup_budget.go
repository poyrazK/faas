package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// primeStartupBootAllowance is the VM boot time a cold boot is allowed on top
// of an extended startup deadline. vmmd starts the readiness clock after the
// guest boots (readyTimeoutFor). On production-us a cold boot took 14 s
// before readiness began.
const primeStartupBootAllowance = 15 * time.Second

// maxStartupExtension is primeStartupExtension for the longest startup
// window any plan allows (api.MaxAppManifestStartupDeadlineS).
const maxStartupExtension = time.Duration(api.MaxAppManifestStartupDeadlineS)*time.Second + primeStartupBootAllowance - ColdBootSweepBudget

// primeStartupExtension is how much longer than the spec §6.1 30 s
// COLD_BOOTING window a cold boot of an app with this startup deadline may
// run. ADR-138 gives each app a startup deadline; ADR-20261114120000 lets a
// plan's startup window (Limits.MaxStartupDeadlineS: 30/30/60/120 s) exceed
// the spec window. The RPC budget (ColdBootTimeout) and the watchdog
// (ColdBootSweepBudget) are 35 s and 30 s, so a Scale app that listened after
// 40 s was killed 30.6 s into its boot ("cold_boot_timeout") on
// production-us. Deadlines within the spec window keep the spec budgets. A
// longer deadline extends both, plus a boot allowance, on the deploy prime
// and on every later cold boot (a wake without a snapshot, or after a
// refused restore). vmmd's own readiness failure, which says why the app was
// not ready, then arrives before the scheduler gives up.
func primeStartupExtension(startupDeadlineS int32) time.Duration {
	deadline := time.Duration(startupDeadlineS) * time.Second
	if deadline <= ColdBootSweepBudget {
		return 0
	}
	return min(deadline, time.Duration(api.MaxAppManifestStartupDeadlineS)*time.Second) + primeStartupBootAllowance - ColdBootSweepBudget
}

// primeColdBootBudget is the vmmd RPC deadline for a deploy prime. The test
// override stays authoritative so deadline tests stay fast.
func (e *Engine) primeColdBootBudget(startupDeadlineS int32) time.Duration {
	if e.bootBudget != nil {
		return e.bootBudget(state.StateColdBooting)
	}
	return ColdBootTimeout + primeStartupExtension(startupDeadlineS)
}

// startupWatchdogExtension reports how long past its sweep budget the
// watchdog must leave a COLD_BOOTING instance, or a WAKING one that fell back
// to a cold boot, of an app with an extended startup deadline. It mirrors the
// RPC budget the engine gave the boot (budgetForWake, primeColdBootBudget).
// A lookup error returns 0, which keeps the spec budget.
func (w *Watchdog) startupWatchdogExtension(ctx context.Context, ins state.Instance) time.Duration {
	if ins.AppID == "" {
		return 0
	}
	app, err := w.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return 0
	}
	acct, err := state.AppResourceAccount(ctx, w.store, app)
	if err != nil {
		return 0
	}
	return primeStartupExtension(startupDeadlineForApp(app, acct.Plan))
}
