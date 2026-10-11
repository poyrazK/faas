package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type ProfileDeploymentChecksView struct {
	Policy      api.ProfileDeploymentPolicy
	Checks      []api.ProfileDeploymentCheck
	Monitors    []api.ProfilePeriodicMonitor
	CSRF, Error string
	CanEdit     bool
}

func (*ProfileDeploymentChecksView) Limits() map[string]any {
	return map[string]any{"gate_timeout_max": api.ProfileGateMaxTimeoutSeconds, "gate_timeout_default": api.ProfileGateDefaultTimeoutSeconds, "gate_confirmations_max": api.ProfileGateMaxConfirmations, "gate_confirmations_default": api.ProfileGateDefaultConfirmations, "periodic_interval_min": api.ProfilePeriodicMinIntervalSeconds, "periodic_interval_max": api.ProfilePeriodicMaxIntervalSeconds, "periodic_interval_default": api.ProfilePeriodicDefaultIntervalSeconds, "periodic_confirmations_max": api.ProfilePeriodicMaxConfirmations, "periodic_confirmations_default": api.ProfilePeriodicDefaultConfirmations, "window_min": api.ProfileAutoMinWindowSeconds, "window_max": api.ProfileAutoMaxWindowSeconds, "warmup_max": api.ProfileAutoMaxWarmupSeconds, "relative": api.ProfileRegressionMaxRelativePercent, "absolute": api.ProfileRegressionMaxAbsoluteCPU, "absolute_per_request": api.ProfileRegressionMaxAbsoluteCPUPerRequest, "profiles": api.ProfileMaxCoverageEntries, "coverage": api.ProfileRegressionMinimumCoverageRatio, "routes": api.ProfileRouteRegressionMaxRoutes, "requests": api.ProfileRegressionMaxMinimumRequests}
}
