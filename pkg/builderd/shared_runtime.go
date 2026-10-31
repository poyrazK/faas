package builderd

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
	"github.com/onebox-faas/faas/pkg/state"
)

// sharedRuntimeBuildLog reports guest-init's shared-runtime classification
// (H10-140) so operators can see how many Node app builds would leave their
// toolchain out of the app layer before imaged acts on it.
func sharedRuntimeBuildLog(v *api.SharedRuntimeVerdict) string {
	switch {
	case v == nil:
		return ""
	case v.Eligible:
		return "shared runtime: eligible (the app layer would need only /app)\n"
	default:
		return "shared runtime: not eligible (" + v.Reason + ")\n"
	}
}

// SharedRuntimeEnv controls building source container apps on the shared
// runner base; see sharedruntime.Env.
const SharedRuntimeEnv = sharedruntime.Env

// sharedSourceAppRuntime picks the runner base a source build of app runs
// on, or "" for the app's own base. Only Node and Python container apps
// without a runtime or a Dockerfile qualify; job-mode boots still assume the
// minimal base. specs are the source's version declarations as written
// (sourceVersionSpecs): the parsed source version drops bare majors such as
// "22". A musl runner base (node22) is never chosen: the build's glibc
// toolchain would not run on it if the app turned out ineligible.
func sharedSourceAppRuntime(app state.App, fw Framework, specs []string, dockerfilePath string, env func(string) string) string {
	if !sharedruntime.Enabled(env(SharedRuntimeEnv)) || app.Type == state.AppTypeFunction || app.Runtime != "" ||
		dockerfilePath != "" || app.Manifest.ExecutionMode == api.ExecutionModeJob {
		return ""
	}
	runtime := ""
	switch fw {
	case FrameworkNode:
		runtime = sharedruntime.NodeRuntimeForSpecs(specs)
	case FrameworkPython:
		runtime = sharedruntime.PythonRuntimeForSpecs(specs)
	}
	if sharedruntime.MuslRunner(runtime) {
		return ""
	}
	return runtime
}

// sourceVersionSpecs reads the version declarations of a Node or Python
// source, including railpack.json's package pin, which the builder's own
// pin would otherwise overwrite.
func sourceVersionSpecs(fw Framework, sourcePath, sourceRoot string) []string {
	var specs []string
	switch fw {
	case FrameworkNode:
		for _, spec := range []string{
			markers.NodeVersionSpecAtRoot(sourcePath, sourceRoot),
			markers.RailpackPackageVersionAtRoot(sourcePath, sourceRoot, "node"),
		} {
			if spec != "" {
				specs = append(specs, spec)
			}
		}
	case FrameworkPython:
		specs = markers.PythonVersionSpecsAtRoot(sourcePath, sourceRoot)
	}
	return specs
}

// claimedBuildRuntime is the runtime a build's export claim signs: the
// shared runner base it was built on, or the app's own runtime.
func claimedBuildRuntime(app state.App, result BuildResult) string {
	if result.Runtime != "" && state.SourceRuntimeClaimAllowed(app, result.Runtime) {
		return result.Runtime
	}
	return app.Runtime
}
