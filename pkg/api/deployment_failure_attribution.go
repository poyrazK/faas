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
	CodeFunctionInitFailed,
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

// Two codes are customer-caused only on their own evidence, which the status
// aggregates read beside error_code (prod 2026-11-22: test apps' migrations
// and a 422 from an app's own route put "deployments degraded" on the public
// status page):
//
//   - CodeReleaseCommandFailed: the release command ran and exited non-zero,
//     timed out or was cancelled by its owner. The platform's own release
//     failures are CodeReleaseInfrastructure. Exit 126 or 127 (command not
//     executable or not found) stays platform evidence: a runtime base or PATH
//     that lost the app's tools fails exactly this way.
//   - CodeDeploymentSmokeFailed with smoke error CustomerCausedSmokeErrorCode:
//     the public check reached the candidate, proved the response came from
//     it, and the app answered with an unacceptable status. Unproven or
//     unreachable checks keep counting.

// PlatformReleaseExitCodes are release-task exit codes that remain platform
// evidence although the command itself ran.
var PlatformReleaseExitCodes = []int{126, 127}

// CustomerCausedSmokeErrorCode is the hosting-receipt smoke error code of an
// authenticated candidate answer with an unacceptable HTTP status.
const CustomerCausedSmokeErrorCode = "smoke_http_status"

// IsCustomerCausedReleaseExit reports whether a release task that ended with
// exitCode (nil: no exit, e.g. a timeout or cancel) failed for the customer's
// command rather than the platform.
func IsCustomerCausedReleaseExit(exitCode *int) bool {
	if exitCode == nil {
		return true
	}
	for _, code := range PlatformReleaseExitCodes {
		if *exitCode == code {
			return false
		}
	}
	return true
}
