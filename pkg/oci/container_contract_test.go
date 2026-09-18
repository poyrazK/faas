package oci

import (
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestContainerDeploymentContract pins the public normal-OCI contract: image
// process metadata is projected without a function adapter, and a non-Gregale
// layer prefix is classified for the full-rootfs path.
func TestContainerDeploymentContract(t *testing.T) {
	manifest, err := ManifestFromConfig(Config{
		Entrypoint:   []string{"/app/server"},
		Cmd:          []string{"--http"},
		Env:          map[string]string{"PORT": "8080"},
		WorkingDir:   "/app",
		User:         "1001",
		ExposedPorts: map[string]struct{}{"8080/tcp": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.Entrypoint, []string{"/app/server", "--http"}) {
		t.Errorf("entrypoint = %v, want [/app/server --http]", manifest.Entrypoint)
	}
	if manifest.Port != api.DefaultAppPort {
		t.Errorf("port = %d, want %d", manifest.Port, api.DefaultAppPort)
	}
	if manifest.Env["PORT"] != "8080" {
		t.Errorf("PORT = %q, want 8080", manifest.Env["PORT"])
	}
	if manifest.WorkingDir != "/app" || manifest.User != "1001" {
		t.Errorf("manifest process fields = (%q, %q), want (/app, 1001)", manifest.WorkingDir, manifest.User)
	}

	_, err = LayersAboveBase(
		[]string{"sha256:gregale-base"},
		[]string{"sha256:foreign-base", "sha256:app"},
	)
	if !errors.Is(err, ErrLayersNotAboveBase) {
		t.Fatalf("LayersAboveBase error = %v, want ErrLayersNotAboveBase", err)
	}
}
