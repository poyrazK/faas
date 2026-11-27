package apidserver

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
	response.RunControls = &api.RunControlCapabilities{ConfigurationAvailable: planEntitled && s.executionAPIEnabled && s.executionV2Enabled, MaxCustomers: api.RunCustomersByPlan[acct.Plan.PlanIndex()], MaxBindings: api.RunBindingsByPlan[acct.Plan.PlanIndex()], HistoryPageSize: api.RunHistoryPageMax}
	response.TaskPipelines = planEntitled && s.executionAPIEnabled && s.executionV2Enabled && s.executionTasksEnabled && s.executionPipelinesEnabled
	response.ExtendedAdmissions = planEntitled && s.executionAPIEnabled && s.executionV2Enabled
	response.ArtifactDeliveries = response.ExtendedAdmissions && s.object.ObjectStorageEnabled()
	if response.ExtendedAdmissions {
		limits := acct.Plan.ExecutionFileLimits()
		response.FileLimits = &limits
		response.MaxContractSchemaBytes = api.ExecutionContractSchemaMaxBytes
	}
	response.CustomEnvironments = response.ExtendedAdmissions && s.executionEnvironmentsEnabled && len(s.executionEnvironmentBases) > 0
	if response.CustomEnvironments {
		for _, runtime := range response.Runtimes {
			if s.executionEnvironmentBases[runtime] != "" {
				response.EnvironmentRuntimes = append(response.EnvironmentRuntimes, runtime)
			}
		}
		limits := acct.Plan.ExecutionEnvironmentLimits()
		response.EnvironmentLimits = &limits
	}
	if planEntitled {
		for _, class := range []api.ExecutionClass{api.ExecutionClassStandard, api.ExecutionClassExtended} {
			limits := planLimits.ForClass(class)
			available := response.AdmissionAvailable
			if class == api.ExecutionClassExtended {
				available = available && s.executionV2Enabled && s.executionExtendedEnabled
			}
			response.ExecutionClasses = append(response.ExecutionClasses, api.ExecutionClassCapability{Class: class, DefaultTimeoutMS: limits.DefaultTimeoutMS, MaxTimeoutMS: limits.MaxTimeoutMS, MaxConcurrent: limits.MaxConcurrent, AdmissionAvailable: available})
		}
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
