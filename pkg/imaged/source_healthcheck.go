package imaged

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceComposeHealthcheck returns the Compose healthcheck a built service
// froze at enqueue (ADR-20261019195000). Nil leaves the built image's own
// HEALTHCHECK unchanged. A stage retry copies the failed deployment's
// profile, so it keeps the override.
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

// recordSourceRuntimeProfile hands the built image's port and effective
// command HEALTHCHECK to the scheduler before snapshot priming. Source
// inference cannot see ports inherited from a base image. Explicit deployment
// overrides remain authoritative, as on the registry-image path.
func (h *Handler) recordSourceRuntimeProfile(ctx context.Context, dep *state.Deployment, manifest api.AppManifest) error {
	required := imageManifestHealthcheckRequired(manifest.Healthcheck)
	profile, _ := persistedProfile(*dep)
	portChanged := dep.OverridePort == 0 && manifest.Port != 0 && profile.Port != manifest.Port
	healthcheckChanged := frameworkprofile.RequiresSourceImageHealthcheck(dep.InferredProfile) != required
	if !portChanged && !healthcheckChanged {
		return nil
	}
	// Merge runtime facts into the profile as stored, keeping fields this binary
	// does not know. A build whose profile inference failed has none; start
	// one the way an image deployment does.
	fields := map[string]json.RawMessage{}
	if len(dep.InferredProfile) != 0 {
		if err := json.Unmarshal(dep.InferredProfile, &fields); err != nil {
			return fmt.Errorf("imaged: decode source runtime profile: %w", err)
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	if _, ok := fields["version"]; !ok {
		fields["version"] = json.RawMessage(strconv.Quote(frameworkprofile.Version))
	}
	if _, ok := fields["framework"]; !ok {
		fields["framework"] = json.RawMessage(`"unknown"`)
	}
	if portChanged {
		fields["port"] = json.RawMessage(strconv.Itoa(manifest.Port))
	}
	if healthcheckChanged {
		fields["image_healthcheck_required"] = json.RawMessage(strconv.FormatBool(required))
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("imaged: encode source runtime profile: %w", err)
	}
	if err := h.store.SetDeploymentRuntimeProfile(ctx, dep.ID, raw); err != nil {
		return fmt.Errorf("imaged: persist source runtime profile: %w", err)
	}
	dep.InferredProfile = raw
	return nil
}
