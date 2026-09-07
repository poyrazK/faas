package imaged

import (
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// persistedProfile returns only the version of the profile contract that this
// binary knows how to apply. Invalid or newer rows fall back to the OCI/image
// defaults; a profile must never make an otherwise deployable artifact fail.
func persistedProfile(dep state.Deployment) (frameworkprofile.Profile, bool) {
	if len(dep.InferredProfile) == 0 {
		return frameworkprofile.Profile{}, false
	}
	var profile frameworkprofile.Profile
	if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil || profile.Version != frameworkprofile.Version {
		return frameworkprofile.Profile{}, false
	}
	return profile, true
}

// applySourceProfile overlays the safe, source-derived runtime defaults onto
// an OCI manifest. Values already present in the image remain authoritative;
// deployment overrides are applied by applyOverrides after this helper.
func applySourceProfile(manifest api.AppManifest, dep state.Deployment) api.AppManifest {
	profile, ok := persistedProfile(dep)
	if !ok {
		return manifest
	}
	if manifest.Port == 0 && profile.Port >= 1 && profile.Port <= 65535 {
		manifest.Port = profile.Port
	}
	if manifest.Healthz == "" && validProfileHealthPath(profile.HealthPath) {
		manifest.Healthz = profile.HealthPath
	}
	return manifest
}

// manifestFromLocalOCIConfig handles the OCI export produced by builderd.
// Railpack normally emits an entrypoint, but a profile-derived command is a
// safe fallback for a valid source tree whose exporter leaves both OCI command
// fields empty.
func manifestFromLocalOCIConfig(config oci.Config, dep state.Deployment) (api.AppManifest, error) {
	if len(config.Entrypoint) == 0 && len(config.Cmd) == 0 {
		if command, ok := profileStartCommand(dep); ok {
			config.Cmd = command
		}
	}
	manifest, err := oci.ManifestFromConfig(config)
	if err != nil {
		return api.AppManifest{}, err
	}
	return applySourceProfile(manifest, dep), nil
}

// manifestFromImageConfigWithApp lets an explicit app command rescue an OCI
// image that omitted both Entrypoint and Cmd, then applies that command with
// the same precedence used by source-built apps.
func manifestFromImageConfigWithApp(config oci.ImageConfig, app state.App) (api.AppManifest, error) {
	if len(config.Entrypoint) == 0 && len(config.Cmd) == 0 {
		if start := strings.TrimSpace(app.StartCommand); start != "" {
			config.Cmd = shellCommand(start)
		}
	}
	manifest, err := manifestFromImageConfig(config)
	if err != nil {
		return api.AppManifest{}, err
	}
	return applyAppStartCommand(manifest, app), nil
}

// applyAppStartCommand enforces the documented app-level command override.
// Profile commands are used only when the built OCI artifact has no command;
// Railpack normally emits an optimized launcher that must be retained.
func applyAppStartCommand(manifest api.AppManifest, app state.App) api.AppManifest {
	if start := strings.TrimSpace(app.StartCommand); start != "" {
		manifest.Entrypoint = shellCommand(start)
	}
	return manifest
}

func profileStartCommand(dep state.Deployment) ([]string, bool) {
	profile, ok := persistedProfile(dep)
	if !ok || !profile.Inferred || strings.TrimSpace(profile.StartCommand) == "" {
		return nil, false
	}
	return shellCommand(profile.StartCommand), true
}

func shellCommand(command string) []string {
	return []string{"/bin/sh", "-c", command}
}

func validProfileHealthPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\r\n")
}
