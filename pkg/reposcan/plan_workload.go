package reposcan

import (
	"maps"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlanWorkload preserves the metadata contract used by server scans and local readiness.
func PlanWorkload(w Workload) api.PlanWorkload {
	policy := api.ServiceBindingPolicy(w.ServiceBindingPolicy).Effective()
	if w.ServiceBindingPolicy == "" {
		policy = api.ServiceBindingPolicyProject
	}
	releaseCommand, releaseSource := PlanReleaseCommand(w)
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
		GoCommand:           w.GoCommand,
		SecretFiles:         api.CloneSecretFiles(w.SecretFiles),
		Resources:           w.Resources.Clone(),
		Command:             w.Command,
		DependsOn:           w.DependsOn,
		Uses:                w.Uses.Clone(),
		DependsOnConditions: maps.Clone(w.DependsOnConditions),

		ReleaseCommand:       releaseCommand,
		ReleaseCommandSource: releaseSource,

		ServiceBindingPolicy:      policy,
		ServiceBindingTransport:   api.ServiceBindingTransport(w.ServiceBindingTransport),
		ServiceReliability:        w.ServiceReliability,
		PreviewServiceCallsPolicy: api.PreviewServiceCallsPolicy(w.PreviewServiceCallsPolicy).Effective(),
		AllowedServiceCallers:     w.AllowedServiceCallers,
		AllowedServiceCallScopes:  w.AllowedServiceCallScopes,
		PlatformTenantRequired:    w.PlatformTenantRequired,
		Visibility:                NewWorkloadVisibility(w),
		Access:                    w.Access,
		AppProtocol:               w.AppProtocol,
		Streaming:                 w.Streaming,
		WakeAhead:                 w.WakeAhead,

		Class:       string(w.Class),
		Schedule:    w.Schedule,
		Ports:       append([]int{}, w.Ports...),
		EnvKeys:     w.EnvKeys,
		SourceHosts: append([]api.PlanSourceHost(nil), w.SourceHosts...),
		Source:      w.Source,
		Tier:        w.Tier.String(),
		DetectedBy:  planDetection(w.DetectedBy),
	}
}

// PlanReleaseCommand renders the release command the scan assigned w and
// where it came from: a Compose service w waits for to complete
// (ADR-20261102192943) or the repository root Procfile's release: process.
func PlanReleaseCommand(w Workload) (string, string) {
	if len(w.ReleaseCommand) == 0 {
		return "", ""
	}
	command := strings.Join(w.ReleaseCommand, " ")
	if w.ReleaseCommandShell {
		command = w.ReleaseCommand[0]
	}
	if w.ReleaseService != "" {
		return command, "Compose service " + w.ReleaseService + " (service_completed_successfully)"
	}
	return command, "Procfile release:"
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
// (ADR-20261022131852), and .gregale/project.yaml can set either way
// (ADR-20261025093331). Empty means public.
func NewWorkloadVisibility(w Workload) api.AppVisibility {
	if w.Unpublished {
		return api.AppVisibilityInternal
	}
	return ""
}
