package reposcan

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// composeStartupDeadlineWarning flags a Compose healthcheck whose
// start_period is longer than the startup deadline the service will get.
//
// start_period is a grace period for failing checks, not a promise of
// startup time: the platform still stops an instance that is not ready by
// its startup deadline, which defaults to the cold-boot window on every
// plan (api.ColdBootReadyWindowS) and is raised only by
// lifecycle.startup_deadline_s (ADR-20261114120000). A JVM service declaring
// start_period: 60s failed with cold_boot_timeout at 30 s and nothing in the
// plan said why (retail-store GREG-01).
//
// The scanned root's gregale.yaml supplies the configured deadline; it applies
// to every service of the project.
func composeStartupDeadlineWarning(fsys fs.FS, check *api.ComposeHealthcheck) string {
	if check == nil || check.StartPeriodNS <= 0 {
		return ""
	}
	startPeriod := time.Duration(check.StartPeriodNS)
	startPeriodS := int((startPeriod + time.Second - 1) / time.Second)
	deadlineS, source := api.ColdBootReadyWindowS, "default startup deadline"
	if configured, ok := composeConfiguredStartupDeadline(fsys); ok {
		deadlineS, source = configured, "lifecycle.startup_deadline_s"
	}
	if startPeriodS <= deadlineS {
		return ""
	}
	return fmt.Sprintf(
		"healthcheck start_period %s is longer than the %d s %s; an instance not ready after %d s is stopped (cold_boot_timeout). "+
			"If it can take that long to start, set lifecycle.startup_deadline_s: %d in gregale.yaml (plan limits: %s)",
		startPeriod, deadlineS, source, deadlineS, startPeriodS, startupDeadlinePlanCaps())
}

func composeConfiguredStartupDeadline(fsys fs.FS) (int, bool) {
	m, found, err := gregalemanifest.LoadFS(fsys, ".")
	if err != nil || !found || m == nil || m.Lifecycle == nil || m.Lifecycle.StartupDeadlineS == nil || *m.Lifecycle.StartupDeadlineS <= 0 {
		return 0, false
	}
	return *m.Lifecycle.StartupDeadlineS, true
}

// startupDeadlinePlanCaps names each plan's longest startup deadline from the
// limits table, e.g. "free 30 s, hobby 30 s, pro 60 s, scale 120 s".
func startupDeadlinePlanCaps() string {
	out := ""
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		limits, ok := api.LimitsFor(plan)
		if !ok || limits.MaxStartupDeadlineS <= 0 {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += fmt.Sprintf("%s %d s", plan, limits.MaxStartupDeadlineS)
	}
	return out
}
