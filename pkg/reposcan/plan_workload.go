package reposcan

import (
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlanWorkload preserves the metadata contract used by server scans and local readiness.
func PlanWorkload(w Workload) api.PlanWorkload {
	policy := api.ServiceBindingPolicy(w.ServiceBindingPolicy).Effective()
	if w.ServiceBindingPolicy == "" {
		policy = api.ServiceBindingPolicyProject
	}
	return api.PlanWorkload{
		Name:                w.Name,
		AppSlug:             w.AppSlug,
		RootDir:             w.RootDir,
		Dockerfile:          w.Dockerfile,
		Image:               w.Image,
		ImageHealthcheck:    w.ImageHealthcheck.Clone(),
		ImageProcess:        w.ImageProcess.Clone(),
		BuildTarget:         w.DockerfileBuild.TargetStage(),
		BuildArgs:           w.DockerfileBuild.ArgNames(),
		SecretFiles:         api.CloneSecretFiles(w.SecretFiles),
		Command:             w.Command,
		DependsOn:           w.DependsOn,
		DependsOnConditions: maps.Clone(w.DependsOnConditions),

		ServiceBindingPolicy:      policy,
		ServiceBindingTransport:   api.ServiceBindingTransport(w.ServiceBindingTransport),
		ServiceReliability:        w.ServiceReliability,
		PreviewServiceCallsPolicy: api.PreviewServiceCallsPolicy(w.PreviewServiceCallsPolicy).Effective(),
		AllowedServiceCallers:     w.AllowedServiceCallers,
		AllowedServiceCallScopes:  w.AllowedServiceCallScopes,
		PlatformTenantRequired:    w.PlatformTenantRequired,
		Visibility:                NewWorkloadVisibility(w),

		Class:      string(w.Class),
		Schedule:   w.Schedule,
		Ports:      append([]int{}, w.Ports...),
		EnvKeys:    w.EnvKeys,
		Source:     w.Source,
		Tier:       w.Tier.String(),
		DetectedBy: planDetection(w.DetectedBy),
	}
}

func planDetection(d Detection) *api.PlanDetectedBy {
	if d.Detector == "" {
		return nil
	}
	return &api.PlanDetectedBy{
		Detector:   d.Detector,
		Marker:     d.Marker,
		Priority:   int(d.Priority),
		MergedFrom: d.MergedFrom,
	}
}

// NewWorkloadVisibility is the edge exposure of a new app for w: a Compose
// service without `ports:` is internal, as Docker publishes nothing for it
// (ADR-20261022131852). Empty means public.
func NewWorkloadVisibility(w Workload) api.AppVisibility {
	if w.Unpublished {
		return api.AppVisibilityInternal
	}
	return ""
}
