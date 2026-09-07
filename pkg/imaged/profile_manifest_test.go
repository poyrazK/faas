package imaged

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func profileDeployment(t *testing.T, profile frameworkprofile.Profile) state.Deployment {
	t.Helper()
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return state.Deployment{InferredProfile: raw}
}

func TestManifestFromLocalOCIConfig_UsesProfileForEmptyOCICommand(t *testing.T) {
	t.Parallel()
	dep := profileDeployment(t, frameworkprofile.Profile{
		Version: frameworkprofile.Version, Framework: "express", StartCommand: "npm run start",
		Port: 3000, HealthPath: "/ready", Inferred: true,
	})
	manifest, err := manifestFromLocalOCIConfig(oci.Config{}, dep)
	if err != nil {
		t.Fatalf("manifestFromLocalOCIConfig: %v", err)
	}
	if want := []string{"/bin/sh", "-c", "npm run start"}; !reflect.DeepEqual(manifest.Entrypoint, want) {
		t.Fatalf("entrypoint = %#v, want %#v", manifest.Entrypoint, want)
	}
	if manifest.Port != 3000 || manifest.Healthz != "/ready" {
		t.Fatalf("profile defaults = port %d, health %q; want 3000, /ready", manifest.Port, manifest.Healthz)
	}
}

func TestManifestFromLocalOCIConfigPreservesOCICommand(t *testing.T) {
	t.Parallel()
	dep := profileDeployment(t, frameworkprofile.Profile{
		Version: frameworkprofile.Version, Framework: "fastapi", StartCommand: "uvicorn app:app",
		Port: 8000, HealthPath: "/healthz", Inferred: true,
	})
	manifest, err := manifestFromLocalOCIConfig(oci.Config{Cmd: []string{"/app/launcher"}}, dep)
	if err != nil {
		t.Fatalf("manifestFromLocalOCIConfig: %v", err)
	}
	if want := []string{"/app/launcher"}; !reflect.DeepEqual(manifest.Entrypoint, want) {
		t.Fatalf("entrypoint = %#v, want %#v", manifest.Entrypoint, want)
	}
	if manifest.Port != 8000 || manifest.Healthz != "/healthz" {
		t.Fatalf("profile defaults = port %d, health %q; want 8000, /healthz", manifest.Port, manifest.Healthz)
	}
}

func TestApplyAppStartCommandUsesExplicitAppCommand(t *testing.T) {
	t.Parallel()
	manifest := applyAppStartCommand(api.AppManifest{Entrypoint: []string{"/app/launcher"}}, state.App{StartCommand: "./serve --port $PORT"})
	want := []string{"/bin/sh", "-c", "./serve --port $PORT"}
	if !reflect.DeepEqual(manifest.Entrypoint, want) {
		t.Fatalf("entrypoint = %#v, want %#v", manifest.Entrypoint, want)
	}
}

func TestManifestFromImageConfigWithAppUsesExplicitCommandWhenImageIsEmpty(t *testing.T) {
	t.Parallel()
	manifest, err := manifestFromImageConfigWithApp(oci.ImageConfig{}, state.App{StartCommand: "./serve --port $PORT"})
	if err != nil {
		t.Fatalf("manifestFromImageConfigWithApp: %v", err)
	}
	want := []string{"/bin/sh", "-c", "./serve --port $PORT"}
	if !reflect.DeepEqual(manifest.Entrypoint, want) {
		t.Fatalf("entrypoint = %#v, want %#v", manifest.Entrypoint, want)
	}
}
