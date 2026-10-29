package imaged

import (
	"context"
	"fmt"
	"os"

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

// layerTooLargeNodeFix returns nodeVersionLayerFix for a deployment whose
// Node source build could have used the shared runtime, or "". It reads the
// source archive, so it runs only for an app layer over the plan cap.
func (h *Handler) layerTooLargeNodeFix(ctx context.Context, depID string) string {
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
	return nodeVersionLayerFix(markers.NodeVersionSpecAtRoot(dep.SourcePath, dep.SourceRoot))
}
