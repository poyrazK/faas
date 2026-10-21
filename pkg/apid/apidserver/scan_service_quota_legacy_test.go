package apidserver

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// evaluateQuotaGate computes the can_apply triple for the given
// (workloads, limits, observedApps, observedCrons). The function
// derives the cron count from workloads (any workload with a
// non-empty Schedule is also a cron — see scan_service.go:714-727
// for the equivalent inline loop) so callers can evaluate the gate
// on both pre-filter and post-filter workloads without sharing
// scan-internal state.
//
// Returns:
//
//	canApply    — true when the plan fits under the plan caps AND
//	               crons are allowed by the plan.
//	notAllowed  — true when crons are forbidden by the plan (the
//	               special hard-block Free plan sees on Hobby/Pro/
//	               Scale cron workloads). The flag is independent
//	               of canApply: a Free-plan operator who uploaded
//	               a cron sees both canApply=false AND
//	               notAllowed=true.
//	reasons     — human-readable list of why can_apply is false.
//	               Empty when can_apply is true. The dashboard
//	               renders these verbatim in the gate card;
//	               CanApplyReasons on PlanResponse carries this
//	               same slice.
//	cronCount   — the count derived from workloads (returned so
//	               the caller can size the partition's
//	               observed_crons wire field without re-walking
//	               workloads).
//
// Pure function; no I/O. Tests pin the three failure-mode shapes
// (apps-over, crons-not-allowed, crons-over) plus the rescue
// invariant (preCanApply=false → postCanApply=true after exclude).
func evaluateQuotaGate(
	workloads []reposcan.Workload,
	limits api.Limits,
	observedApps int,
	observedCrons int,
) (canApply bool, notAllowed bool, reasons []string, cronCount int) {
	for _, wl := range workloads {
		workloadCronCount := len(wl.CronSchedules())
		cronCount += workloadCronCount
		if limits.CronLimitPerApp > 0 && workloadCronCount > limits.CronLimitPerApp {
			canApply = false
			reasons = append(reasons, fmt.Sprintf(
				"crons over per-app limit for %s: %d > %d",
				wl.Name, workloadCronCount, limits.CronLimitPerApp,
			))
		}
	}

	if len(reasons) == 0 {
		canApply = true
	}
	if observedApps+len(workloads) > limits.DeployedApps {
		canApply = false
		reasons = append(reasons, fmt.Sprintf(
			"apps over plan limit: %d + %d > %d",
			observedApps, len(workloads), limits.DeployedApps,
		))
	}
	if cronCount > 0 && limits.CronLimitPerAccount == 0 {
		canApply = false
		notAllowed = true
		reasons = append(reasons, "crons not allowed on this plan")
	}
	if observedCrons+cronCount > limits.CronLimitPerAccount {
		canApply = false
		reasons = append(reasons, fmt.Sprintf(
			"crons over plan limit: %d + %d > %d",
			observedCrons, cronCount, limits.CronLimitPerAccount,
		))
	}
	return
}
