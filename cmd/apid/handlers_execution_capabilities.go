package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionprofiles"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getExecutionCapabilities(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	planLimits, planKnown := acct.Plan.ExecutionLimits()
	planEntitled := planKnown && planLimits.Allowed
	response := api.ExecutionCapabilitiesResponse{
		Plan:                string(acct.Plan),
		AdmissionAvailable:  planEntitled && s.executionAPIEnabled,
		PlanEntitled:        planEntitled,
		ControlPlaneEnabled: s.executionAPIEnabled,
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
			}, Packages: executionprofiles.Packages(api.ExecutionProfilePythonDataV1)},
		},
		NetworkModes: []api.ExecutionNetworkMode{api.ExecutionNetworkNone},
	}
	if !planEntitled {
		response.UnavailableReasons = append(response.UnavailableReasons, "plan_not_entitled")
	}
	if !s.executionAPIEnabled {
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
	writeJSON(w, http.StatusOK, response)
}
