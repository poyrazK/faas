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
	FunctionRuntimeBinary,
}

// FunctionRuntimeBinary runs one statically linked Linux executable, built
// from the function's Dockerfile, in any compiled language (ADR-987). It has
// no base image of its own: it runs on the go124 base and runner.
const FunctionRuntimeBinary = "binary"

// FunctionBaseRuntime returns the runtime whose base image and runner serve
// runtime. Only binary differs: it shares go124's.
func FunctionBaseRuntime(runtime string) string {
	if runtime == FunctionRuntimeBinary {
		return "go124"
	}
	return runtime
}

// FunctionRuntimeBuildsFromDockerfile reports whether a function runtime is
// built from the customer's Dockerfile. Other function runtimes use the
// platform's language builds and refuse a Dockerfile.
func FunctionRuntimeBuildsFromDockerfile(runtime string) bool {
	return runtime == FunctionRuntimeBinary
}

// FunctionDockerfileProblem enforces the function source rule (ADR-987): a
// binary function needs a Dockerfile whose image names one static
// executable, and every other runtime refuses one. Nil means acceptable.
func FunctionDockerfileProblem(runtime string, hasDockerfile bool) *Problem {
	fromDockerfile := FunctionRuntimeBuildsFromDockerfile(runtime)
	switch {
	case fromDockerfile && !hasDockerfile:
		return NewProblem(http.StatusBadRequest, CodeValidation, "Dockerfile required",
			"binary functions are built from a Dockerfile whose ENTRYPOINT is one static executable inside /app; add a Dockerfile at the source root")
	case !fromDockerfile && hasDockerfile:
		return NewProblem(http.StatusBadRequest, CodeValidation, "Dockerfile functions unsupported",
			"function deploys use the platform runtime scaffold; remove the Dockerfile and dockerfile=true, or use runtime binary to build a compiled executable from it")
	}
	return nil
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

// Project images may name a tag; imaged pins the resolved source before
// materializing it. References must first be normalized with oci.ParseReference.
var projectImageRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]+)?/[A-Za-z0-9_./-]+(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[0-9a-f]{64})?$`)

// ValidProjectImage validates a normalized registry reference with a tag or
// digest. Unlike direct image API admission, project tags are pinned by imaged.
func ValidProjectImage(ref string) bool {
	if !projectImageRE.MatchString(ref) {
		return false
	}
	name := strings.SplitN(ref, "@", 2)[0]
	if colon := strings.LastIndexByte(name, ':'); colon > strings.LastIndexByte(name, '/') {
		name = name[:colon]
	}
	for _, segment := range strings.Split(name, "/")[1:] {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

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
