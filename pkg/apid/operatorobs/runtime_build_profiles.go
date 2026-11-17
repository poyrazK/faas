package operatorobs

import "github.com/onebox-faas/faas/pkg/api"

// Build profiles evolve independently of the minimal telemetry-input roles.
// V1 freezes the original coverage; v2 adds the build and image services.
type runtimeBuildJobSpec struct {
	job, metric, daemon string
	discovery           bool
}

func RuntimeBuildRoles() []string {
	return []string{"single-box-v1", "control-plane-v1", "single-box-v2", "control-plane-v2"}
}

func RuntimeBuildRoleValid(role string) bool {
	n := len(runtimeBuildJobSpecs(role))
	return n > 0 && n <= api.ObsRuntimeBuildJobsMax
}

func runtimeBuildJobSpecs(role string) []runtimeBuildJobSpec {
	base := []runtimeBuildJobSpec{{"apid", "apid_daemon_build_info", "apid", false}, {"gatewayd", "gatewayd_public_daemon_build_info", "gatewayd-public", false}, {"schedd", "schedd_daemon_build_info", "schedd", false}}
	switch role {
	case "single-box-v1":
		return append(base, runtimeBuildJobSpec{"vmmd", "vmmd_daemon_build_info", "vmmd", false})
	case "control-plane-v1":
		return append(base, runtimeBuildJobSpec{"gatewayd-internal", "gatewayd_daemon_build_info", "gatewayd-internal", true}, runtimeBuildJobSpec{"schedd-compute", "schedd_daemon_build_info", "schedd", true}, runtimeBuildJobSpec{"vmmd", "vmmd_daemon_build_info", "vmmd", true})
	case "single-box-v2":
		return append(runtimeBuildJobSpecs("single-box-v1"), runtimeBuildJobSpec{"builderd", "builderd_daemon_build_info", "builderd", false}, runtimeBuildJobSpec{"imaged", "imaged_daemon_build_info", "imaged", false})
	case "control-plane-v2":
		return append(runtimeBuildJobSpecs("control-plane-v1"), runtimeBuildJobSpec{"builderd", "builderd_daemon_build_info", "builderd", true}, runtimeBuildJobSpec{"imaged", "imaged_daemon_build_info", "imaged", true})
	default:
		return nil
	}
}
