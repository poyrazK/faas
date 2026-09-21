package main

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func effectiveHealthPath(path string) string {
	if path == "" {
		return "/healthz"
	}
	if path[0] != '/' {
		return "/" + path
	}
	return path
}

func cloneWorkloadPorts(ports []api.WorkloadPort) []api.WorkloadPort {
	if ports == nil {
		return nil
	}
	out := make([]api.WorkloadPort, len(ports))
	copy(out, ports)
	return out
}

func lifecycleProblem(plan api.Plan, manifest api.AppManifest, maxConcurrency int) *api.Problem {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid lifecycle configuration", fmt.Sprintf("unknown plan %q", plan))
	}
	maxRequestTimeoutS := int(limits.RequestBudgetMaxDuration().Seconds())
	if manifest.RequestTimeoutS < 0 || manifest.RequestTimeoutS > maxRequestTimeoutS {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid request timeout",
			fmt.Sprintf("request_timeout_s must be between 0 and %d for the %s plan", maxRequestTimeoutS, plan))
	}
	if manifest.HealthPathWakes && !plan.HealthPathWakesAllowed() {
		return api.NewProblem(http.StatusForbidden,
			api.CodePlanHealthPathWakesNotAllowed,
			"Health-path wakes are not allowed on this plan",
			"health_path_wakes requires Pro or Scale; upgrade to use real health probes")
	}
	if err := manifest.ValidateCrawlerPolicy(); err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid crawler policy", err.Error())
	}
	if err := manifest.ValidateLifecyclePlan(plan); err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid lifecycle configuration", err.Error())
	}
	if manifest.ServiceReplicas != nil {
		effectiveMax := maxConcurrency
		if effectiveMax <= 0 || effectiveMax > limits.MaxConcurrency {
			effectiveMax = limits.MaxConcurrency
		}
		if manifest.ServiceReplicas.Desired > effectiveMax {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid lifecycle configuration",
				fmt.Sprintf("service_replicas.desired %d exceeds app max_concurrency %d; increase max_concurrency before raising the service target", manifest.ServiceReplicas.Desired, effectiveMax))
		}
	}
	return nil
}

func lifecycleManifestFromCreate(req api.CreateAppRequest) api.AppManifest {
	healthPath := req.HealthPath
	if healthPath == "" {
		healthPath = "/healthz"
	}
	var stopGrace time.Duration
	if req.StopGracePeriodS > 0 {
		stopGrace = time.Duration(req.StopGracePeriodS) * time.Second
	}
	return api.AppManifest{
		ExecutionMode:    req.ExecutionMode,
		RestartPolicy:    req.RestartPolicy,
		StartupDeadlineS: req.StartupDeadlineS,
		MaxRetries:       req.MaxRetries,
		StopGracePeriod:  stopGrace,
		StopSignal:       req.StopSignal,
		RequestTimeoutS:  req.RequestTimeoutS,
		ServiceReplicas:  req.ServiceReplicas,
		WorkerReplicas:   req.WorkerReplicas,
		Ports:            cloneWorkloadPorts(req.Ports),
		Favicon:          append([]byte(nil), req.Favicon...),
		RobotsTxt:        req.RobotsTxt,
		HeadWakes:        req.HeadWakes,
		CrawlerPolicy:    req.CrawlerPolicy,
		HealthPath:       healthPath,
		HealthPathWakes:  req.HealthPathWakes,
		SessionAffinity:  req.SessionAffinity != nil && *req.SessionAffinity,
	}
}

func stateManifestFromAPI(manifest api.AppManifest) state.AppManifest {
	var replicas *state.ServiceReplicas
	if manifest.ServiceReplicas != nil {
		replicas = &state.ServiceReplicas{
			Min: manifest.ServiceReplicas.Min, Max: manifest.ServiceReplicas.Max,
			Desired: manifest.ServiceReplicas.Desired,
		}
	}
	var workerReplicas *state.WorkerScaling
	if manifest.WorkerReplicas != nil {
		workerReplicas = &state.WorkerScaling{
			Min: manifest.WorkerReplicas.Min, Max: manifest.WorkerReplicas.Max,
			Metric: manifest.WorkerReplicas.Metric, Target: manifest.WorkerReplicas.Target,
		}
	}
	stopGracePeriodS := 0
	if manifest.StopGracePeriod > 0 {
		stopGracePeriodS = int(math.Ceil(manifest.StopGracePeriod.Seconds()))
	}
	return state.AppManifest{
		ExecutionMode:    manifest.ExecutionMode,
		RestartPolicy:    manifest.RestartPolicy,
		StartupDeadlineS: manifest.StartupDeadlineS,
		MaxRetries:       manifest.MaxRetries,
		StopGracePeriodS: stopGracePeriodS,
		StopSignal:       manifest.StopSignal,
		RequestTimeoutS:  manifest.RequestTimeoutS,
		ServiceReplicas:  replicas,
		WorkerReplicas:   workerReplicas,
		Ports:            cloneWorkloadPorts(manifest.Ports),
		Favicon:          append([]byte(nil), manifest.Favicon...),
		RobotsTxt:        manifest.RobotsTxt,
		HeadWakes:        manifest.HeadWakes,
		CrawlerPolicy:    manifest.CrawlerPolicy,
		HealthPath:       manifest.HealthPath,
		HealthPathWakes:  manifest.HealthPathWakes,
		SessionAffinity:  manifest.SessionAffinity,
	}
}

func apiManifestFromState(manifest state.AppManifest) api.AppManifest {
	var replicas *api.ServiceReplicas
	if manifest.ServiceReplicas != nil {
		replicas = &api.ServiceReplicas{
			Min: manifest.ServiceReplicas.Min, Max: manifest.ServiceReplicas.Max,
			Desired: manifest.ServiceReplicas.Desired,
		}
	}
	var workerReplicas *api.WorkerScaling
	if manifest.WorkerReplicas != nil {
		workerReplicas = &api.WorkerScaling{
			Min: manifest.WorkerReplicas.Min, Max: manifest.WorkerReplicas.Max,
			Metric: manifest.WorkerReplicas.Metric, Target: manifest.WorkerReplicas.Target,
		}
	}
	var stopGrace time.Duration
	if manifest.StopGracePeriodS > 0 {
		stopGrace = time.Duration(manifest.StopGracePeriodS) * time.Second
	}
	return api.AppManifest{
		ExecutionMode:    manifest.ExecutionMode,
		RestartPolicy:    manifest.RestartPolicy,
		StartupDeadlineS: manifest.StartupDeadlineS,
		MaxRetries:       manifest.MaxRetries,
		StopGracePeriod:  stopGrace,
		StopSignal:       manifest.StopSignal,
		RequestTimeoutS:  manifest.RequestTimeoutS,
		ServiceReplicas:  replicas,
		WorkerReplicas:   workerReplicas,
		Ports:            cloneWorkloadPorts(manifest.Ports),
		Favicon:          append([]byte(nil), manifest.Favicon...),
		RobotsTxt:        manifest.RobotsTxt,
		HeadWakes:        manifest.HeadWakes,
		CrawlerPolicy:    manifest.CrawlerPolicy,
		HealthPath:       manifest.HealthPath,
		HealthPathWakes:  manifest.HealthPathWakes,
		SessionAffinity:  manifest.SessionAffinity,
	}
}

func mergedLifecycleManifest(app state.App, req *api.UpdateAppRequest) (api.AppManifest, bool) {
	changed := req.ExecutionMode != nil || req.RestartPolicy != nil ||
		req.StartupDeadlineS != nil || req.MaxRetries != nil || req.RequestTimeoutS != nil || req.ServiceReplicas != nil ||
		req.WorkerReplicas != nil || req.StopGracePeriodS != nil || req.StopSignal != nil ||
		req.Favicon != nil || req.RobotsTxt != nil || req.HeadWakes != nil || req.CrawlerPolicy != nil ||
		req.HealthPath != nil || req.HealthPathWakes != nil || req.SessionAffinity != nil || req.Ports != nil
	if !changed {
		return api.AppManifest{}, false
	}
	manifest := apiManifestFromState(app.Manifest)
	if req.ExecutionMode != nil {
		manifest.ExecutionMode = *req.ExecutionMode
	}
	if req.RestartPolicy != nil {
		manifest.RestartPolicy = *req.RestartPolicy
	}
	if req.StartupDeadlineS != nil {
		manifest.StartupDeadlineS = *req.StartupDeadlineS
	}
	if req.MaxRetries != nil {
		manifest.MaxRetries = *req.MaxRetries
	}
	if req.StopGracePeriodS != nil {
		manifest.StopGracePeriod = time.Duration(*req.StopGracePeriodS) * time.Second
	}
	if req.StopSignal != nil {
		manifest.StopSignal = *req.StopSignal
	}
	if req.RequestTimeoutS != nil {
		manifest.RequestTimeoutS = *req.RequestTimeoutS
	}
	if req.ServiceReplicas != nil {
		manifest.ServiceReplicas = req.ServiceReplicas
	} else if manifest.EffectiveExecutionMode() != api.ExecutionModeService {
		manifest.ServiceReplicas = nil
	}
	if req.WorkerReplicas != nil {
		manifest.WorkerReplicas = req.WorkerReplicas
	} else if manifest.EffectiveExecutionMode() != api.ExecutionModeWorker {
		manifest.WorkerReplicas = nil
	}
	if req.Ports != nil {
		manifest.Ports = cloneWorkloadPorts(*req.Ports)
	}
	if req.Favicon != nil {
		manifest.Favicon = append([]byte(nil), (*req.Favicon)...)
	}
	if req.RobotsTxt != nil {
		manifest.RobotsTxt = *req.RobotsTxt
	}
	if req.HeadWakes != nil {
		manifest.HeadWakes = *req.HeadWakes
	}
	if req.CrawlerPolicy != nil {
		manifest.CrawlerPolicy = *req.CrawlerPolicy
	}
	if req.HealthPath != nil {
		manifest.HealthPath = *req.HealthPath
		if manifest.HealthPath == "" {
			manifest.HealthPath = "/healthz"
		}
	}
	if req.HealthPathWakes != nil {
		manifest.HealthPathWakes = *req.HealthPathWakes
	}
	if req.SessionAffinity != nil {
		manifest.SessionAffinity = *req.SessionAffinity
	}
	return manifest, true
}

func stateManifestForUpdate(app state.App, req *api.UpdateAppRequest) (*state.AppManifest, bool) {
	manifest, changed := mergedLifecycleManifest(app, req)
	if !changed {
		return nil, false
	}
	updated := app.Manifest
	updated.ExecutionMode = manifest.ExecutionMode
	updated.RestartPolicy = manifest.RestartPolicy
	updated.StartupDeadlineS = manifest.StartupDeadlineS
	updated.MaxRetries = manifest.MaxRetries
	updated.StopGracePeriodS = stateManifestFromAPI(manifest).StopGracePeriodS
	updated.StopSignal = manifest.StopSignal
	updated.RequestTimeoutS = manifest.RequestTimeoutS
	updated.ServiceReplicas = stateManifestFromAPI(manifest).ServiceReplicas
	updated.WorkerReplicas = stateManifestFromAPI(manifest).WorkerReplicas
	updated.Ports = cloneWorkloadPorts(manifest.Ports)
	updated.Favicon = append([]byte(nil), manifest.Favicon...)
	updated.RobotsTxt = manifest.RobotsTxt
	updated.HeadWakes = manifest.HeadWakes
	updated.CrawlerPolicy = manifest.CrawlerPolicy
	updated.HealthPath = manifest.HealthPath
	updated.HealthPathWakes = manifest.HealthPathWakes
	updated.SessionAffinity = manifest.SessionAffinity
	return &updated, true
}
