package executionprofiles

import "github.com/onebox-faas/faas/pkg/api"

// Capabilities is the Runs capability document for a plan. apid serves it
// with its host flag; a contained rehearsal serves it as enabled, because its
// starts are captured rather than admitted (ADR-20261022170348).
func Capabilities(plan api.Plan, controlPlaneEnabled bool) api.ExecutionCapabilitiesResponse {
	planLimits, planKnown := plan.ExecutionLimits()
	planEntitled := planKnown && planLimits.Allowed
	response := api.ExecutionCapabilitiesResponse{
		Plan:                string(plan),
		AdmissionAvailable:  planEntitled && controlPlaneEnabled,
		PlanEntitled:        planEntitled,
		ControlPlaneEnabled: controlPlaneEnabled,
		Runtimes: []api.ExecutionRuntime{
			api.ExecutionRuntimeNode22,
			api.ExecutionRuntimeNode24,
			api.ExecutionRuntimePython312,
			api.ExecutionRuntimePython313,
		},
		Profiles: []api.ExecutionProfileCapability{
			{Profile: api.ExecutionProfileStandard, Runtimes: []api.ExecutionRuntime{
				api.ExecutionRuntimeNode22,
				api.ExecutionRuntimeNode24,
				api.ExecutionRuntimePython312,
				api.ExecutionRuntimePython313,
			}},
			{Profile: api.ExecutionProfilePythonDataV1, Runtimes: []api.ExecutionRuntime{
				api.ExecutionRuntimePython313,
			}, Packages: Packages(api.ExecutionProfilePythonDataV1)},
		},
		NetworkModes: []api.ExecutionNetworkMode{api.ExecutionNetworkNone},
	}
	if !planEntitled {
		response.UnavailableReasons = append(response.UnavailableReasons, "plan_not_entitled")
	}
	if !controlPlaneEnabled {
		response.UnavailableReasons = append(response.UnavailableReasons, "control_plane_disabled")
	}
	if planEntitled {
		response.Limits = &api.ExecutionCapabilityLimits{
			MaxConcurrentRuns:      planLimits.MaxConcurrent,
			MaxSourceBytes:         planLimits.MaxSourceBytes,
			MaxInputBytes:          planLimits.MaxInputBytes,
			DefaultOutputBytes:     planLimits.DefaultOutputBytes,
			MaxOutputBytes:         planLimits.MaxOutputBytes,
			DefaultTimeoutMS:       planLimits.DefaultTimeoutMS,
			MaxTimeoutMS:           planLimits.MaxTimeoutMS,
			DefaultMemoryMB:        planLimits.DefaultMemoryMB,
			MaxMemoryMB:            planLimits.MaxMemoryMB,
			DefaultCPUMillicores:   planLimits.DefaultCPUMillicores,
			MaxCPUMillicores:       planLimits.MaxCPUMillicores,
			DefaultEphemeralDiskMB: planLimits.DefaultEphemeralDiskMB,
			MaxEphemeralDiskMB:     planLimits.MaxEphemeralDiskMB,
			PIDsMax:                planLimits.PIDsMax,
			MaxBundleFiles:         api.ExecutionBundleMaxFiles,
			MaxArtifactInputs:      api.ExecutionArtifactInputMaxFiles,
			MaxOutputFiles:         api.ExecutionArtifactMaxFiles,
			MaxArtifactPathBytes:   api.ExecutionArtifactMaxPathBytes,
		}
	}
	return response
}
