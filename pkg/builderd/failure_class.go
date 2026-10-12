package builderd

// failure_class.go — build-failure classification.
//
// Deliberately UNTAGGED even though its only caller is the metal VM driver:
// the logic is a JSON read plus an exit-code table with no KVM, jailer or
// Firecracker dependency, and keeping it behind `//go:build metal && linux`
// would mean ordinary CI never executes it. That is how the misclassification
// in #2577 survived — a build that never ran was reported as the customer's
// fault, and nothing outside the hardware gate could have caught it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// classifyBuildFailure resolves the failure class for a non-zero build exit.
// It prefers BuildDone.FailureClass (guest-init's classification) when
// /build-done.json exists in the export, then falls back to the canonical
// exit-code table (137→OOM, 124→Timeout, else UserError). Without a valid
// guest completion marker, ordinary host exit codes are infrastructure failures:
// Firecracker can exit 1 before it has evaluated any customer source. The vocabulary
// here matches the canonical names used by pkg/state.FailureClass:
// "FailureUserError" / "FailureInfra" / "FailureOOM" / "FailureTimeout".
// builderd.go's ProcessOne translates these to the column-friendly
// strings ("oom" etc) at the state.Store boundary.
//
// Error-explanations cluster (spec §6.4 amendment 1): the second
// return value is the RFC 7807 stable code guest-init stamped on
// BuildDone.FailureCode (app_arch_mismatch / dep_install_failed),
// plus the package manager discriminator for dep_install_failed
// (npm / pip / go / cargo). Empty strings when guest-init fell back
// to the coarse FailureClass only — the caller stamps the legacy
// CodeDeployFailed path.
func classifyBuildFailure(exitCode int, exportDir string) (string, string, string) {
	done := filepath.Join(exportDir, "build-done.json")
	if data, err := os.ReadFile(done); err == nil {
		var bd api.BuildDone
		if json.Unmarshal(data, &bd) == nil && bd.SchemaVersion == 1 && bd.BuildID != "" {
			if bd.FailureClass != "" {
				return bd.FailureClass, bd.FailureCode, bd.FailurePkg
			}
			return classifyGuestBuildExit(bd.ExitCode), "", ""
		}
	}
	if exitCode == 137 || exitCode == 124 {
		return classifyGuestBuildExit(exitCode), "", ""
	}
	return "FailureInfra", "", ""
}

func classifyGuestBuildExit(exitCode int) string {
	switch {
	case exitCode == 137:
		return "FailureOOM"
	case exitCode == 124:
		return "FailureTimeout"
	case exitCode < 0:
		// No exit status at all: the VM never ran to completion, so nothing
		// about the customer's source has been evaluated yet. Blaming the user
		// here sends operators looking at build logs for a platform fault —
		// which is exactly what the missing-digest-sidecar bug (#2577) did,
		// reporting failure_class=user_error for "vm exit -1" after the spawn
		// failed on a host path builderd resolved incorrectly.
		return "FailureInfra"
	default:
		return "FailureUserError"
	}
}

// buildOutputFailurePatterns map the customer's own build output to the
// error-explanations codes. guest-init only reports a coarse failure class and
// never stamps BuildDone.FailureCode, so without this host-side reading
// dep_install_failed and app_arch_mismatch (spec §6.4 amendment 1) could never
// fire: a missing npm package failed as a bare "build exited 1" (production-us
// hunt #9).
var buildOutputFailurePatterns = []struct {
	needles []string
	code    string
	pkg     string
}{
	{needles: []string{"exec format error"}, code: api.CodeAppArchMismatch},
	{needles: []string{"err_pnpm_"}, code: api.CodeDepInstallFailed, pkg: "pnpm"},
	{needles: []string{"npm error code e404", "npm err! code e404", "npm error code etarget", "npm err! code etarget", "npm error code eresolve", "npm err! code eresolve", "npm error 404 ", "npm err! 404 "}, code: api.CodeDepInstallFailed, pkg: "npm"},
	{needles: []string{"error an unexpected error occurred: \"https://registry.yarnpkg.com", "couldn't find any versions for", "doesn't exist in the registry"}, code: api.CodeDepInstallFailed, pkg: "yarn"},
	{needles: []string{"could not find a version that satisfies the requirement", "no matching distribution found for"}, code: api.CodeDepInstallFailed, pkg: "pip"},
	{needles: []string{"no matching package named", "failed to select a version for the requirement"}, code: api.CodeDepInstallFailed, pkg: "cargo"},
}

// classifyBuildOutput returns the error-explanations code (and package
// manager) a failed build's own output proves, or "" when nothing matches.
// Only the customer's [build] section is read: buildkitd's daemon log repeats
// generic exec errors for every failure.
func classifyBuildOutput(logTail string) (code, pkg string) {
	build, _, _ := strings.Cut(logTail, "\n[buildkitd]\n")
	lower := strings.ToLower(build)
	for _, pattern := range buildOutputFailurePatterns {
		for _, needle := range pattern.needles {
			if strings.Contains(lower, needle) {
				return pattern.code, pattern.pkg
			}
		}
	}
	if goModuleFailure(lower) {
		return api.CodeDepInstallFailed, "go"
	}
	return "", ""
}

func goModuleFailure(lower string) bool {
	if !strings.Contains(lower, "go: ") {
		return false
	}
	for _, needle := range []string{"unknown revision", "invalid version", "module lookup disabled", "cannot find module providing package", "reading https://proxy.golang.org", "missing go.sum entry"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
