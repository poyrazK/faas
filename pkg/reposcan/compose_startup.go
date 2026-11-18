package reposcan

import (
	"fmt"
	"io/fs"
	"strconv"
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
//
// Without gregale.yaml the service's own deadline comes from the file
// (composeStartupDeadline), so only a configured project deadline can be
// shorter than the start_period; that case still warns.
func composeStartupDeadlineWarning(fsys fs.FS, check *api.ComposeHealthcheck) string {
	if check == nil || check.StartPeriodNS <= 0 {
		return ""
	}
	startPeriod := time.Duration(check.StartPeriodNS)
	startPeriodS := ceilSeconds(startPeriod)
	configured, ok := composeConfiguredStartupDeadline(fsys)
	if !ok || startPeriodS <= configured {
		return ""
	}
	deadlineS, source := configured, "lifecycle.startup_deadline_s"
	return fmt.Sprintf(
		"healthcheck start_period %s is longer than the %d s %s; an instance not ready after %d s is stopped (cold_boot_timeout). "+
			"If it can take that long to start, set lifecycle.startup_deadline_s: %d in gregale.yaml (plan limits: %s)",
		startPeriod, deadlineS, source, deadlineS, startPeriodS, startupDeadlinePlanCaps())
}

// Docker's healthcheck defaults, used to read how long a Compose service says
// it may take to become healthy.
const (
	composeHealthcheckDefaultInterval = 30 * time.Second
	composeHealthcheckDefaultRetries  = 3
)

// composeStartupDeadline returns the startup deadline, in seconds, a Compose
// service asks for, and a note for the plan; 0 keeps the platform default
// (retail-store feedback A5, 2026-11-17).
//
// x-gregale-startup-deadline (seconds or a duration) is explicit. Otherwise a
// healthcheck with a start_period declares how long the service may take:
// Docker marks it unhealthy only after start_period plus retries failed
// checks one interval apart, so that sum becomes the deadline when it is
// longer than the default cold-boot window. A project-wide
// lifecycle.startup_deadline_s in gregale.yaml takes precedence over the
// derived value. Apply caps the result at the account plan's maximum.
func composeStartupDeadline(fsys fs.FS, raw any, check *api.ComposeHealthcheck) (int, string, error) {
	if raw != nil {
		seconds, err := composeDeadlineSeconds(raw)
		if err != nil {
			return 0, "", fmt.Errorf("x-gregale-startup-deadline %w", err)
		}
		return seconds, fmt.Sprintf("startup deadline %d s from x-gregale-startup-deadline (plan limits: %s)", seconds, startupDeadlinePlanCaps()), nil
	}
	if check == nil || check.StartPeriodNS <= 0 {
		return 0, "", nil
	}
	if _, ok := composeConfiguredStartupDeadline(fsys); ok {
		return 0, "", nil
	}
	interval := time.Duration(check.IntervalNS)
	if interval <= 0 {
		interval = composeHealthcheckDefaultInterval
	}
	retries := check.Retries
	if retries <= 0 {
		retries = composeHealthcheckDefaultRetries
	}
	seconds := min(ceilSeconds(time.Duration(check.StartPeriodNS)+time.Duration(retries)*interval), api.MaxAppManifestStartupDeadlineS)
	if seconds <= api.ColdBootReadyWindowS {
		return 0, "", nil
	}
	return seconds, fmt.Sprintf("startup deadline raised to %d s from the healthcheck (start_period %s + %d retries × %s), up to the plan limit (%s); set x-gregale-startup-deadline to choose another",
		seconds, time.Duration(check.StartPeriodNS), retries, interval, startupDeadlinePlanCaps()), nil
}

func composeDeadlineSeconds(raw any) (int, error) {
	var seconds int
	switch v := raw.(type) {
	case int:
		seconds = v
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				return 0, fmt.Errorf("must be seconds or a duration such as 90s")
			}
			d = time.Duration(n) * time.Second
		}
		seconds = ceilSeconds(d)
	default:
		return 0, fmt.Errorf("must be seconds or a duration such as 90s")
	}
	if seconds < 1 || seconds > api.MaxAppManifestStartupDeadlineS {
		return 0, fmt.Errorf("must be between 1 and %d seconds", api.MaxAppManifestStartupDeadlineS)
	}
	return seconds, nil
}

func ceilSeconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
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
