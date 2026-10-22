package imaged

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceComposeHealthcheck returns the Compose healthcheck a built service
// froze at enqueue (ADR-20261019195000). Nil leaves the built image's own
// HEALTHCHECK, and its readiness, exactly as before. A stage retry copies the
// failed deployment's profile, so it keeps the override.
func sourceComposeHealthcheck(dep state.Deployment) (*api.ComposeHealthcheck, error) {
	check, err := frameworkprofile.ImageHealthcheckFromProfile(dep.InferredProfile)
	if err != nil || check == nil {
		return nil, err
	}
	return check.Override, nil
}

// applySourceComposeHealthcheck merges the override onto the built image's
// HEALTHCHECK with the rules a prebuilt image uses.
func applySourceComposeHealthcheck(config oci.Config, override *api.ComposeHealthcheck) (oci.Config, error) {
	if override == nil {
		return config, nil
	}
	applied, err := oci.ApplyComposeHealthcheck(oci.ImageConfig{Healthcheck: config.Healthcheck}, override)
	if err != nil {
		return oci.Config{}, err
	}
	config.Healthcheck = applied.Healthcheck
	return config, nil
}

// recordSourceHealthcheck stores imaged's verdict beside the frozen override.
// The scheduler then requires a fresh command check before readiness, and
// drives recovery from it, exactly as for a prebuilt image (ADR-683, ADR-684).
func (h *Handler) recordSourceHealthcheck(ctx context.Context, dep *state.Deployment, manifest api.AppManifest) error {
	var profile frameworkprofile.Profile
	if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil {
		return fmt.Errorf("imaged: decode source runtime profile: %w", err)
	}
	required := imageManifestHealthcheckRequired(manifest.Healthcheck)
	if profile.ImageHealthcheckRequired == required && frameworkprofile.RequiresSourceImageHealthcheck(dep.InferredProfile) == required {
		return nil
	}
	profile.ImageHealthcheckRequired = required
	raw, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("imaged: encode source runtime profile: %w", err)
	}
	if err := h.store.SetDeploymentRuntimeProfile(ctx, dep.ID, raw); err != nil {
		return fmt.Errorf("imaged: persist source runtime profile: %w", err)
	}
	dep.InferredProfile = raw
	return nil
}
