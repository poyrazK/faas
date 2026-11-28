package api

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

// customerCausedDeploymentFailureCodes are deployments.error_code values whose
// detection evidence places the failure in the customer's own workload or
// content: the guest kernel refused the readiness connection, the workload
// crashed or ran out of its plan memory, the build could not install the
// customer's dependencies, or a policy gate rejected what the customer
// shipped: its app layer is over the plan's filesystem cap, or its GitHub
// branch moved past the commit being deployed (prod 2026-11-28: two Free
// signups' refusals put "deployments degraded" on the public status page).
// Codes whose evidence can also come from the platform (app_startup_timeout
// when the guest never answered, deployment_smoke_failed, stage_* timeouts,
// internal, source_ref_unavailable when GitHub could not be asked,
// image_manifest_invalid) are deliberately absent.
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
	CodeAppLayerTooBig,
	CodeSourceRefStale,
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
//     stays platform evidence only when its stderr names a runtime tool the
//     platform provides (PlatformToolMissingPattern): a runtime base or PATH
//     that lost npm or python fails exactly this way, while a typo in the
//     customer's own command ("nmp: not found", prod 2026-11-23) is theirs.
//     A task with no exit and no owner cancel
//     (a platform cancel, a failure code imaged does not classify) also
//     stays platform evidence.
//   - CodeDeploymentSmokeFailed with smoke error CustomerCausedSmokeErrorCode
//     and a 4xx status: the public check proved the response came from the
//     candidate and the app refused the request. A proven 5xx can come from
//     the platform's own bridge hop inside the guest, so it keeps counting,
//     as do unproven or unreachable checks.

// PlatformReleaseExitCodes are release-task exit codes that remain platform
// evidence although the task ended with an exit: when guest-init could not
// start the command (PlatformReleaseStartFailureCodes, or no stderr at all),
// or the task's stderr matches PlatformToolMissingPattern.
var PlatformReleaseExitCodes = []int{126, 127}

// PlatformReleaseStartFailureCodes are app-task failure codes guest-init
// records with exit 126/127 when the command never ran: its executable was
// not found or its working directory could not be prepared
// (guest/init/app_task_linux.go). Their stderr is empty.
var PlatformReleaseStartFailureCodes = []string{"command_not_found", "command_start_failed"}

// PlatformToolMissingPattern matches a shell or env(1) report that a runtime
// tool the platform's bases provide is missing or not executable:
// "/bin/sh: 1: npm: not found", "/usr/bin/env: 'node': No such file or
// directory". The tool must stand as a bare word: a customer's own path that
// ends in a tool name ("./.venv/bin/python: not found") is theirs. It is a
// POSIX-style pattern both Go's regexp and PostgreSQL's ~ accept, so the
// status aggregate and the stores agree.
const PlatformToolMissingPattern = `(^|[^A-Za-z0-9_./-])'?(npm|npx|node|nodejs|corepack|yarn|pnpm|bun|python|python3|pip|pip3|uv|uvx|poetry|pipenv|mise|sh|bash)'?(: not found|: command not found|: No such file or directory|: Permission denied)`

var platformToolMissing = lazyregexp.New(PlatformToolMissingPattern)

// platformReleaseExit reports whether an exit-126/127 release task's evidence
// points at the platform.
func platformReleaseExit(failureCode, stderr string) bool {
	for _, code := range PlatformReleaseStartFailureCodes {
		if failureCode == code {
			return true
		}
	}
	return strings.TrimSpace(stderr) == "" || platformToolMissing.MatchString(stderr)
}

// StartupCleanExitMarker is the text vmmd puts in an app_startup_crash
// detail when the app's process exited with status 0: it ran to completion
// instead of serving. The CLI keys a specific line off it, because the
// catalog copy says the app "crashed" (prod 2026-11-23: a script that printed
// "done" and a Dockerfile without CMD both read as crashes).
const StartupCleanExitMarker = "the app's process exited with status 0 (a clean exit is not restarted)"

// CustomerCausedSmokeErrorCode is the hosting-receipt smoke error code of an
// authenticated candidate answer with an unacceptable HTTP status.
const CustomerCausedSmokeErrorCode = "smoke_http_status"

// IsCustomerCausedReleaseTask reports whether a failed release task ended
// for the customer's command rather than the platform: an exit other than
// the platform failing to start it or a platform tool going missing
// (failureCode is the task's failure code, stderr its captured stderr tail),
// the command's own timeout, or a cancel its owner requested.
func IsCustomerCausedReleaseTask(exitCode *int, failureCode, stderr string, timedOut, ownerCancelled bool) bool {
	if exitCode != nil {
		for _, code := range PlatformReleaseExitCodes {
			if *exitCode == code && platformReleaseExit(failureCode, stderr) {
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
