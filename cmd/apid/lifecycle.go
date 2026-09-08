package main

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func lifecycleProblem(plan api.Plan, manifest api.AppManifest, maxConcurrency int) *api.Problem {
	if err := manifest.ValidateCrawlerPolicy(); err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid crawler policy", err.Error())
	}
	if err := manifest.ValidateLifecyclePlan(plan); err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid lifecycle configuration", err.Error())
	}
	if manifest.ServiceReplicas != nil {
		limits, ok := api.LimitsFor(plan)
		if !ok {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid lifecycle configuration", fmt.Sprintf("unknown plan %q", plan))
		}
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
	return api.AppManifest{
		ExecutionMode:    req.ExecutionMode,
		RestartPolicy:    req.RestartPolicy,
		StartupDeadlineS: req.StartupDeadlineS,
		MaxRetries:       req.MaxRetries,
		ServiceReplicas:  req.ServiceReplicas,
		Favicon:          append([]byte(nil), req.Favicon...),
		RobotsTxt:        req.RobotsTxt,
		HeadWakes:        req.HeadWakes,
		CrawlerPolicy:    req.CrawlerPolicy,
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
	return state.AppManifest{
		ExecutionMode:    manifest.ExecutionMode,
		RestartPolicy:    manifest.RestartPolicy,
		StartupDeadlineS: manifest.StartupDeadlineS,
		MaxRetries:       manifest.MaxRetries,
		ServiceReplicas:  replicas,
		Favicon:          append([]byte(nil), manifest.Favicon...),
		RobotsTxt:        manifest.RobotsTxt,
		HeadWakes:        manifest.HeadWakes,
		CrawlerPolicy:    manifest.CrawlerPolicy,
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
	return api.AppManifest{
		ExecutionMode:    manifest.ExecutionMode,
		RestartPolicy:    manifest.RestartPolicy,
		StartupDeadlineS: manifest.StartupDeadlineS,
		MaxRetries:       manifest.MaxRetries,
		ServiceReplicas:  replicas,
		Favicon:          append([]byte(nil), manifest.Favicon...),
		RobotsTxt:        manifest.RobotsTxt,
		HeadWakes:        manifest.HeadWakes,
		CrawlerPolicy:    manifest.CrawlerPolicy,
	}
}

func mergedLifecycleManifest(app state.App, req *api.UpdateAppRequest) (api.AppManifest, bool) {
	changed := req.ExecutionMode != nil || req.RestartPolicy != nil ||
		req.StartupDeadlineS != nil || req.MaxRetries != nil || req.ServiceReplicas != nil ||
		req.Favicon != nil || req.RobotsTxt != nil || req.HeadWakes != nil || req.CrawlerPolicy != nil
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
	if req.ServiceReplicas != nil {
		manifest.ServiceReplicas = req.ServiceReplicas
	} else if manifest.EffectiveExecutionMode() != api.ExecutionModeService {
		manifest.ServiceReplicas = nil
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
	updated.ServiceReplicas = stateManifestFromAPI(manifest).ServiceReplicas
	updated.Favicon = append([]byte(nil), manifest.Favicon...)
	updated.RobotsTxt = manifest.RobotsTxt
	updated.HeadWakes = manifest.HeadWakes
	updated.CrawlerPolicy = manifest.CrawlerPolicy
	return &updated, true
}
