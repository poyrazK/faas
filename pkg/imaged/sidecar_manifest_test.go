package imaged

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

func TestSidecarWorkloadManifest_ProjectsImageAndOverrides(t *testing.T) {
	got, err := (&Handler{}).sidecarWorkloadManifest(api.Sidecar{
		Name: "metrics",
		Cmd:  []string{"--port", "9000"},
		Env: map[string]string{
			// Env contains sealed-at-rest values in the deployment record.
			// sidecarWorkloadManifest must not interpret or persist them.
			"TOKEN": "opaque-ciphertext",
		},
		Port: 9090,
	}, oci.ImageConfig{
		Entrypoint: []string{"/bin/server"},
		Cmd:        []string{"--default"},
		Env:        map[string]string{"TOKEN": "image-value", "MODE": "prod"},
		WorkingDir: "/srv",
		User:       "1001",
	})
	if err != nil {
		t.Fatalf("sidecarWorkloadManifest: %v", err)
	}
	if want := []string{"/bin/server", "--port", "9000"}; !reflect.DeepEqual(got.Entrypoint, want) {
		t.Fatalf("entrypoint = %#v, want %#v", got.Entrypoint, want)
	}
	if got.Env["TOKEN"] != "image-value" || got.Env["MODE"] != "prod" {
		t.Fatalf("env = %#v, want image defaults only", got.Env)
	}
	if got.Env["TOKEN"] == "opaque-ciphertext" {
		t.Fatal("sealed deployment env was copied into immutable image manifest")
	}
	if got.Port != 9090 || got.WorkingDir != "/srv" || got.User != "1001" {
		t.Fatalf("manifest metadata = %#v, want port/workdir/user preserved", got)
	}
}
