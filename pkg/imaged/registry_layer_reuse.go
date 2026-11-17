package imaged

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Registry images used to be converted on every deployment, including
// companions, even when the digest was the one converted minutes before
// (Kroki feedback 2026-11-17). Unsigned registry images now share the source
// path's layer reuse (ADR-20261112170911 amendment 3): the key is computed
// from the resolved digest, the runtime base's diff IDs and the manifest,
// before any layer blob is pulled.

const (
	reuseKindRegistryApp = "registry-app"
	reuseKindCompanion   = "companion"
)

// registryReuseEligible reports whether a registry conversion may be reused.
// A signed (verified) conversion publishes per-layer consumption evidence a
// reuse does not produce, so it always converts.
func registryReuseEligible(app state.App, prepared preparedContainerWorkload) bool {
	return prepared.Verification.ID == "" && prepared.Digest != "" &&
		!app.RequireSigned && !app.SecurityPolicy.RequiresSignedImage()
}

// registryAppLayerReuseKey is the reuse key of an unsigned registry image's
// above-base app layer, or "" when it has none. The above-base split depends
// on the runtime base, so the base's diff IDs are part of the key.
func (h *Handler) registryAppLayerReuseKey(ctx context.Context, app state.App, acct state.Account, prepared preparedContainerWorkload, manifest api.AppManifest) string {
	if h.layerReuse == nil || !registryReuseEligible(app, prepared) {
		return ""
	}
	_, baseDiffIDs, err := h.runtimeBaseDiffIDs(ctx, app.Runtime, "")
	if err != nil {
		return ""
	}
	return h.layerReuseKeyFor(app, layerReuseInputs{kind: reuseKindRegistryApp, accountID: app.AccountID,
		imageDigest: prepared.Digest, runtime: app.Runtime, runtimeBaseDiffIDs: baseDiffIDs, manifest: manifest, plan: acct.Plan})
}

// reuseRegistryAppLayer republishes (or patches) the recorded above-base
// layer of an unsigned registry image and stamps it as the deployment's
// rootfs. It returns the key a build should record under ("" when not
// eligible), whether the layer is done, and a stamping error.
func (h *Handler) reuseRegistryAppLayer(ctx context.Context, app state.App, dep state.Deployment, acct state.Account, prepared preparedContainerWorkload, manifest api.AppManifest, be storage.StorageBackend, key string) (string, bool, error) {
	reuseKey := h.registryAppLayerReuseKey(ctx, app, acct, prepared, manifest)
	if reuseKey == "" {
		return "", false, nil
	}
	result, ok := h.reuseAppLayer(ctx, reuseKey, app, dep, rootfs.BuildInput{
		Manifest: manifest, Storage: be, StorageKey: key, SBOMStorageKey: h.sbomStorageKeyForDeployment(ctx, dep.ID),
	})
	if !ok {
		return reuseKey, false, nil
	}
	h.updateBuildProvenanceSBOM(ctx, dep.ID, result.SBOMKey)
	if err := h.publishContainerRootfs(ctx, app, dep, prepared, "", "app-layer", key, result); err != nil {
		return reuseKey, false, fmt.Errorf("imaged: stamp reused rootfs: %w", err)
	}
	if err := h.replicateLayer(ctx, key); err != nil {
		return reuseKey, false, err
	}
	return reuseKey, true, nil
}

// companionLayerReuseKey is the reuse key of an unsigned companion image's
// layer. A companion's own runtime contract is written into its layer, so
// the whole workload manifest is in the key and a changed one converts.
func (h *Handler) companionLayerReuseKey(app state.App, acct state.Account, prepared preparedContainerWorkload, name string, workload api.AppManifest) string {
	if h.layerReuse == nil || !registryReuseEligible(app, prepared) {
		return ""
	}
	contract, err := json.Marshal(workload)
	if err != nil {
		return ""
	}
	return h.layerReuseKeyFor(app, layerReuseInputs{kind: reuseKindCompanion + ":" + name, accountID: app.AccountID,
		imageDigest: prepared.Digest, startCommand: string(contract), manifest: api.SidecarBuildManifest(), plan: acct.Plan})
}
