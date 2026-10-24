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

// recordSourceHealthcheck stores whether the built image's effective
// HEALTHCHECK, from its Dockerfile or a frozen Compose override, is a command
// check. When it is, the scheduler requires a fresh pass before readiness and
// drives recovery from it, exactly as for a prebuilt image (ADR-683, ADR-684,
// ADR-20261024100000). A build without one writes nothing.
func (h *Handler) recordSourceHealthcheck(ctx context.Context, dep *state.Deployment, manifest api.AppManifest) error {
	required := imageManifestHealthcheckRequired(manifest.Healthcheck)
	if frameworkprofile.RequiresSourceImageHealthcheck(dep.InferredProfile) == required {
		return nil
	}
	// Merge the verdict into the profile as stored, keeping fields this binary
	// does not know. A build whose profile inference failed has none; start
	// one the way an image deployment does.
	fields := map[string]json.RawMessage{}
	if len(dep.InferredProfile) != 0 {
		if err := json.Unmarshal(dep.InferredProfile, &fields); err != nil {
			return fmt.Errorf("imaged: decode source runtime profile: %w", err)
		}
	}
	if _, ok := fields["version"]; !ok {
		fields["version"] = json.RawMessage(strconv.Quote(frameworkprofile.Version))
	}
	if _, ok := fields["framework"]; !ok {
		fields["framework"] = json.RawMessage(`"unknown"`)
	}
	fields["image_healthcheck_required"] = json.RawMessage(strconv.FormatBool(required))
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
