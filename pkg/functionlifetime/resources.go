package functionlifetime

import "github.com/onebox-faas/faas/pkg/api"

const (
	NodeResourceArgument = "--gregale-postgres-lifecycle-v1"
	BeforeResourcePath   = "/__gregale/lifecycle/before-checkpoint"
	AfterResourcePath    = "/__gregale/lifecycle/after-restore"
)

func NativeNode(runtime string) bool {
	return NativeQualificationBuild && (runtime == "node22" || runtime == "node24")
}

// ConfigureResources affects only dedicated native acceptance artifacts.
// Keep explicit customer callbacks; the adapter orders resource ownership
// around them and handles the default endpoints without calling user code.
func ConfigureResources(manifest *api.AppManifest, runtime string) {
	if !NativeNode(runtime) {
		return
	}
	if manifest.BeforeCheckpoint == nil {
		manifest.BeforeCheckpoint = &api.BeforeCheckpointHook{Path: BeforeResourcePath, TimeoutMS: api.AfterRestoreHookMaxTimeoutMS}
	}
	if manifest.AfterRestore == nil {
		manifest.AfterRestore = &api.AfterRestoreHook{Path: AfterResourcePath, TimeoutMS: api.AfterRestoreHookMaxTimeoutMS}
	}
}
