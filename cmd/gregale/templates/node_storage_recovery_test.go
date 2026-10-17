package templates

import (
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestObjectStorageNodeRestoreManifest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "starter")
	if err := Materialize("object-storage-node", dest); err != nil {
		t.Fatal(err)
	}
	manifest, present, err := gregalemanifest.Load(dest)
	if err != nil || !present || manifest == nil {
		t.Fatalf("load storage manifest: %v, present=%v", err, present)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.Hosting == nil || manifest.Hosting.Health != "/readyz" || manifest.Lifecycle == nil || manifest.Release != nil {
		t.Fatalf("missing storage adoption/restore readiness: %+v", manifest)
	}
	hook := manifest.Lifecycle.ToAPI().AfterRestore
	if hook == nil || hook.Path != "/internal/after-restore" || hook.TimeoutMS != 2000 {
		t.Fatalf("restore hook = %#v", hook)
	}
}
