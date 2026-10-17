package api

// customerCausedDeploymentFailureCodes are deployments.error_code values whose
// detection evidence places the failure in the customer's own workload or
// content: the guest kernel refused the readiness connection, the workload
// crashed or ran out of its plan memory, the build could not install the
// customer's dependencies, or a policy gate rejected what the customer
// shipped. Codes whose evidence can also come from the platform
// (app_startup_timeout when the guest never answered, deployment_smoke_failed,
// stage_* timeouts, internal) are deliberately absent.
var customerCausedDeploymentFailureCodes = []string{
	CodeAppNotListening,
	CodeAppLoopbackBound,
	CodeAppArchMismatch,
	CodeEnvVarMissing,
	CodeAppHealthzUnauthorized,
	CodeAppRuntimeOOM,
	CodeDepInstallFailed,
	CodeAppStartupCrash,
	CodeExecutionModeConflict,
	CodeStatelessOnlyViolation,
	CodeSecurityScanBlocked,
	CodeAPIContractBreakingChange,
}

// CustomerCausedDeploymentFailureCodes returns a copy of the error codes that
// platform health aggregates exclude (spec §12 SLOs: build success is measured
// over non-user_error outcomes). A tenant shipping a broken app must not be
// able to degrade the public status page.
func CustomerCausedDeploymentFailureCodes() []string {
	return append([]string(nil), customerCausedDeploymentFailureCodes...)
}

// IsCustomerCausedDeploymentFailure reports whether a failed deployment's
// error code places the failure in the customer's workload or content.
func IsCustomerCausedDeploymentFailure(code string) bool {
	for _, c := range customerCausedDeploymentFailureCodes {
		if c == code {
			return true
		}
	}
	return false
}
