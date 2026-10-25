// adr: 20261023090000
package imaged

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplyAppLifecycleCopiesSecretFiles(t *testing.T) {
	files := []api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password", Mode: 0o400}}
	app := state.App{Manifest: state.AppManifest{ProjectSecretFiles: files}}
	manifest := applyAppLifecycle(api.AppManifest{Entrypoint: []string{"/app"}}, app)
	if !reflect.DeepEqual(manifest.SecretFiles, files) {
		t.Fatalf("secret files = %+v, want %+v", manifest.SecretFiles, files)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	files[0].Name = "edited"
	if manifest.SecretFiles[0].Name != "db_password" {
		t.Fatal("manifest shares the app's slice")
	}
	// The app is the source of truth: an image cannot declare its own.
	image := api.AppManifest{Entrypoint: []string{"/app"}, SecretFiles: []api.SecretFile{{Secret: "X", Name: "x"}}}
	if got := applyAppLifecycle(image, state.App{}).SecretFiles; got != nil {
		t.Fatalf("image-declared secret files kept: %+v", got)
	}
}
