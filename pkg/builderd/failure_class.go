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

	"github.com/onebox-faas/faas/pkg/api"
)

// classifyBuildFailure resolves the failure class for a non-zero build exit.
// It prefers BuildDone.FailureClass (guest-init's classification) when
// /build-done.json exists in the export, then falls back to the canonical
// exit-code table (137→OOM, 124→Timeout, else UserError). The vocabulary
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
		if json.Unmarshal(data, &bd) == nil && bd.FailureClass != "" {
			return bd.FailureClass, bd.FailureCode, bd.FailurePkg
		}
	}
	switch {
	case exitCode == 137:
		return "FailureOOM", "", ""
	case exitCode == 124:
		return "FailureTimeout", "", ""
	case exitCode < 0:
		// No exit status at all: the VM never ran to completion, so nothing
		// about the customer's source has been evaluated yet. Blaming the user
		// here sends operators looking at build logs for a platform fault —
		// which is exactly what the missing-digest-sidecar bug (#2577) did,
		// reporting failure_class=user_error for "vm exit -1" after the spawn
		// failed on a host path builderd resolved incorrectly.
		return "FailureInfra", "", ""
	default:
		return "FailureUserError", "", ""
	}
}
