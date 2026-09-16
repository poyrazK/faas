package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// FunctionRuntimes is the create-time closed set for function apps. Keep this
// in the public API package so CLI preview and apid admission cannot drift.
var FunctionRuntimes = []string{
	"node22",
	"python312",
	"go124",
	"go124-alpine",
	"node24",
	"python313",
}

// ValidateExistingAppShape enforces the immutable app class and function
// runtime contract shared by deploy preview and the existing-app apply path.
// Empty stored fields are tolerated for legacy/test responses that predate
// those read fields; current apid responses always populate them.
func ValidateExistingAppShape(existingType, existingRuntime, requestedType, requestedRuntime string) (string, *Problem) {
	if existingType != "" && requestedType != "" && existingType != requestedType {
		return "app.class", NewProblem(http.StatusConflict, CodeValidation,
			"App class is immutable", fmt.Sprintf("existing app has class %q; deploy requested %q", existingType, requestedType))
	}
	if requestedType == "function" && existingRuntime != "" && requestedRuntime != "" && existingRuntime != requestedRuntime {
		return "app.runtime", NewProblem(http.StatusConflict, CodeValidation,
			"Function runtime is immutable", fmt.Sprintf("existing function uses runtime %q; deploy requested %q", existingRuntime, requestedRuntime))
	}
	return "", nil
}

// ValidFunctionRuntime reports whether runtime is accepted by CreateApp for a
// function-shaped workload.
func ValidFunctionRuntime(runtime string) bool {
	for _, candidate := range FunctionRuntimes {
		if runtime == candidate {
			return true
		}
	}
	return false
}

// deploymentImageRE is the canonical digest-pinned OCI reference grammar used
// by deploy preview and every deployment admission path.
var deploymentImageRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]+)?/[A-Za-z0-9_./-]+@sha256:[0-9a-f]{64}$`)

// ValidDeploymentImage reports whether ref is a full digest-pinned OCI image
// reference. Tags, shortened digests, whitespace and control bytes fail.
func ValidDeploymentImage(ref string) bool {
	return deploymentImageRE.MatchString(ref)
}

// DeploymentImageDigest returns the immutable sha256 digest portion of a
// validated deployment image reference.
func DeploymentImageDigest(ref string) (string, bool) {
	if !ValidDeploymentImage(ref) {
		return "", false
	}
	return ref[strings.IndexByte(ref, '@'):], true
}
