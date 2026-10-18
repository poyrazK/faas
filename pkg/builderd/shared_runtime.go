package builderd

import (
	"github.com/onebox-faas/faas/pkg/api"
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

// SharedRuntimeEnv enables building source container apps on the shared
// runner base (docs/adr/1500-shared-runtime-source-apps.md). Off by
// default until the native-metal acceptance run passes.
const SharedRuntimeEnv = "FAAS_SOURCE_APP_SHARED_RUNTIME"

// sharedSourceAppRuntime picks the runner base a source build of app runs
// on, or "" for the app's own base. Only Node container apps without a
// runtime or a Dockerfile qualify; job-mode boots still assume the minimal
// base. nodeSpec is the source's Node declaration as written (.nvmrc or
// engines.node): the parsed source version drops bare majors such as "22".
func sharedSourceAppRuntime(app state.App, fw Framework, nodeSpec, dockerfilePath string, env func(string) string) string {
	if env(SharedRuntimeEnv) != "1" || app.Type == state.AppTypeFunction || app.Runtime != "" ||
		fw != FrameworkNode || dockerfilePath != "" || app.Manifest.ExecutionMode == api.ExecutionModeJob {
		return ""
	}
	return sharedruntime.NodeRuntimeForSpec(nodeSpec)
}

// claimedBuildRuntime is the runtime a build's export claim signs: the
// shared runner base it was built on, or the app's own runtime.
func claimedBuildRuntime(app state.App, result BuildResult) string {
	if result.Runtime != "" && state.SourceRuntimeClaimAllowed(app, result.Runtime) {
		return result.Runtime
	}
	return app.Runtime
}
