package imaged

import (
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// applySourceBuildProcess applies a built service's frozen Compose user,
// working_dir and stop contract (ADR-20261022160000) to the built image's
// config, as a prebuilt image's overrides are applied. The entrypoint is
// not applied here: the scan folded it into the service's start command.
func applySourceBuildProcess(config oci.Config, dep state.Deployment) oci.Config {
	profile, ok := persistedProfile(dep)
	if !ok || profile.BuildProcess == nil {
		return config
	}
	process := profile.BuildProcess
	if process.User != "" {
		config.User = process.User
	}
	if process.WorkingDir != "" {
		config.WorkingDir = process.WorkingDir
	}
	if process.StopSignal != "" {
		config.StopSignal = process.StopSignal
	}
	if process.StopGracePeriodS != nil {
		config.StopGracePeriodS = *process.StopGracePeriodS
	}
	return config
}
