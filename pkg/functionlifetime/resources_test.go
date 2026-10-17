package functionlifetime

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFunctionResourceManifestGate(t *testing.T) {
	for _, runtime := range []string{"node22", "node24", "python312", ""} {
		manifest := api.AppManifest{}
		ConfigureResources(&manifest, runtime)
		if NativeNode(runtime) {
			if manifest.BeforeCheckpoint == nil || manifest.AfterRestore == nil || manifest.BeforeCheckpoint.Path != BeforeResourcePath || manifest.AfterRestore.Path != AfterResourcePath {
				t.Fatalf("native %s has no automatic lifecycle", runtime)
			}
		} else if manifest.BeforeCheckpoint != nil || manifest.AfterRestore != nil {
			t.Fatalf("ordinary or unsupported %s activated lifecycle", runtime)
		}
		manifest.BeforeCheckpoint = &api.BeforeCheckpointHook{Path: "/customer-before"}
		manifest.AfterRestore = &api.AfterRestoreHook{Path: "/customer-after"}
		ConfigureResources(&manifest, runtime)
		if manifest.BeforeCheckpoint.Path != "/customer-before" || manifest.AfterRestore.Path != "/customer-after" {
			t.Fatal("automatic resources replaced explicit callbacks")
		}
	}
}
