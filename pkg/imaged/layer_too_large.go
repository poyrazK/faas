package imaged

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
	"github.com/onebox-faas/faas/pkg/state"
)

// nodeVersionLayerFix explains an app layer over the plan cap for a Node
// source app whose declared version kept its toolchain in the layer
// (ADR-20261029103512 amendment 1). spec is the declaration as written
// (.nvmrc or engines.node). An undeclared version already builds on the
// shared runtime, so it returns "" for it and for Node 24.
func nodeVersionLayerFix(spec string) string {
	if spec == "" {
		return ""
	}
	runtime := sharedruntime.NodeRuntimeForSpec(spec)
	switch {
	case sharedruntime.MuslRunner(runtime):
		return `• this app declares Node 22, so its layer carries a whole Node toolchain: the shared Node 22 base cannot run Node builds. Declare Node 24 ("engines": {"node": "24"}, or 24 in .nvmrc) to build on the shared runtime, where the layer holds only your app`
	case runtime == "":
		return fmt.Sprintf(`• this app declares Node %q, which has no shared runtime, so its layer carries a whole Node toolchain. Declare Node 24 ("engines": {"node": "24"}, or 24 in .nvmrc) to build on the shared runtime, where the layer holds only your app`, spec)
	default:
		return ""
	}
}

// pythonVersionLayerFix is nodeVersionLayerFix for a Python source app
// (ADR-1500 amendment 2). specs are its declarations as written; none means
// it already built on the shared runtime.
func pythonVersionLayerFix(specs []string) string {
	if len(specs) == 0 || sharedruntime.PythonRuntimeForSpecs(specs) != "" {
		return ""
	}
	return fmt.Sprintf(`• this app declares Python %s, which has no shared runtime, so its layer carries a whole Python toolchain. Declare Python 3.12 or 3.13 (in .python-version) to build on the shared runtime, where the layer holds only your app and its packages`, quotedList(specs))
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

// layerTooLargeVersionFix returns nodeVersionLayerFix or
// pythonVersionLayerFix for a deployment whose source build could have used
// the shared runtime, or "". It reads the source archive, so it runs only for
// an app layer over the plan cap.
func (h *Handler) layerTooLargeVersionFix(ctx context.Context, depID string) string {
	if !sharedruntime.Enabled(os.Getenv(sharedruntime.Env)) {
		return ""
	}
	dep, err := h.store.DeploymentByID(ctx, depID)
	if err != nil || dep.RuntimeBase != "" || dep.SourcePath == "" {
		return ""
	}
	switch dep.Kind {
	case state.DeploymentKindTarball, state.DeploymentKindGitHub, state.DeploymentKindPreview:
	default:
		return ""
	}
	// The shared runtime's own exclusions (builderd's sharedSourceAppRuntime).
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil || app.Type == state.AppTypeFunction || app.Runtime != "" ||
		app.Manifest.BuildDockerfile != "" || app.Manifest.ExecutionMode == api.ExecutionModeJob {
		return ""
	}
	fw, err := markers.DetectFromTarballAtRoot(dep.SourcePath, dep.SourceRoot)
	if err != nil {
		return ""
	}
	switch fw {
	case markers.FrameworkNode:
		return nodeVersionLayerFix(markers.NodeVersionSpecAtRoot(dep.SourcePath, dep.SourceRoot))
	case markers.FrameworkPython:
		return pythonVersionLayerFix(markers.PythonVersionSpecsAtRoot(dep.SourcePath, dep.SourceRoot))
	}
	return ""
}
