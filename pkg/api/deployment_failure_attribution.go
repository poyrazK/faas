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
//   - CodeReleaseCommandFailed: only when the release task shows the command
//     itself ended it: it exited non-zero, hit its own timeout, or its owner
//     cancelled it. Exit 126 or 127 (command not executable or not found)
//     stays platform evidence: a runtime base or PATH that lost the app's
//     tools fails exactly this way. A task with no exit and no owner cancel
//     (a platform cancel, a failure code imaged does not classify) also
//     stays platform evidence.
//   - CodeDeploymentSmokeFailed with smoke error CustomerCausedSmokeErrorCode
//     and a 4xx status: the public check proved the response came from the
//     candidate and the app refused the request. A proven 5xx can come from
//     the platform's own bridge hop inside the guest, so it keeps counting,
//     as do unproven or unreachable checks.

// PlatformReleaseExitCodes are release-task exit codes that remain platform
// evidence although the command itself ran.
var PlatformReleaseExitCodes = []int{126, 127}

// CustomerCausedSmokeErrorCode is the hosting-receipt smoke error code of an
// authenticated candidate answer with an unacceptable HTTP status.
const CustomerCausedSmokeErrorCode = "smoke_http_status"

// IsCustomerCausedReleaseTask reports whether a failed release task ended
// for the customer's command rather than the platform: a non-platform exit
// code, the command's own timeout, or a cancel its owner requested.
func IsCustomerCausedReleaseTask(exitCode *int, timedOut, ownerCancelled bool) bool {
	if exitCode != nil {
		for _, code := range PlatformReleaseExitCodes {
			if *exitCode == code {
				return false
			}
		}
		return true
	}
	return timedOut || ownerCancelled
}

// IsCustomerCausedSmokeStatus reports whether a proven smoke answer with
// status is the app refusing the request (a 4xx) rather than a possible
// platform hop failure.
func IsCustomerCausedSmokeStatus(status int) bool {
	return status >= 400 && status < 500
}
