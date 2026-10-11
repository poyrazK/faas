package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// primeStartupBootAllowance is the VM boot time a deploy prime is allowed on
// top of an extended startup deadline. vmmd starts the readiness clock after
// the guest boots (readyTimeoutFor). On production-us a cold boot took 14 s
// before readiness began.
const primeStartupBootAllowance = 15 * time.Second

// primeStartupExtension is how much longer than the spec §6.1 30 s
// COLD_BOOTING window a deploy prime may run. ADR-138 gives each plan a
// startup deadline (15/30/60/120 s by default, up to 300 s). The prime RPC
// budget (ColdBootTimeout) and the watchdog (ColdBootSweepBudget) were fixed
// at 35 s and 30 s, so a Scale app that listened after 40 s was killed 30.6 s
// into its first boot ("cold_boot_timeout") on production-us. Deadlines
// within the spec window keep the spec budgets. A longer deadline extends
// both, plus a boot allowance. vmmd's own readiness failure, which says why
// the app was not ready, then arrives before the scheduler gives up.
func primeStartupExtension(startupDeadlineS int32) time.Duration {
	deadline := time.Duration(startupDeadlineS) * time.Second
	if deadline <= ColdBootSweepBudget {
		return 0
	}
	return deadline + primeStartupBootAllowance - ColdBootSweepBudget
}

// primeColdBootBudget is the vmmd RPC deadline for a deploy prime. The test
// override stays authoritative so deadline tests stay fast.
func (e *Engine) primeColdBootBudget(startupDeadlineS int32) time.Duration {
	if e.bootBudget != nil {
		return e.bootBudget(state.StateColdBooting)
	}
	return ColdBootTimeout + primeStartupExtension(startupDeadlineS)
}

// primeWatchdogExtension reports how long past ColdBootSweepBudget the
// watchdog must leave a COLD_BOOTING instance that is priming a deployment
// (status snapshotting) whose app has an extended startup deadline. Ordinary
// wakes keep the spec budget: they run on the scheduler's main loop and their
// RPC deadline is not extended. A lookup error returns 0, which keeps the
// spec budget.
func (w *Watchdog) primeWatchdogExtension(ctx context.Context, ins state.Instance) time.Duration {
	if ins.DeploymentID == "" || ins.AppID == "" {
		return 0
	}
	dep, err := w.store.DeploymentByID(ctx, ins.DeploymentID)
	if err != nil || dep.Status != state.DeploySnapshotting {
		return 0
	}
	app, err := w.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return 0
	}
	acct, err := w.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return 0
	}
	return primeStartupExtension(startupDeadlineForApp(app, acct.Plan))
}
