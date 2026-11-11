package imaged

import (
	"context"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// sandboxTemplateShell is a template build's placeholder entrypoint when its
// image names none: sessions never run the image's command, but their
// commands run through a shell.
const sandboxTemplateShell = "/bin/sh"

// publishSandboxTemplateRootfs publishes a sandbox template's build
// (ADR-20261027090013) as a full root filesystem: every layer of the built
// image with the platform's full-rootfs marker, booted as drive1 over the
// agent base. Its app runs in the job execution mode, so schedd verifies the
// artifact and marks the build live without booting it.
func (h *Handler) publishSandboxTemplateRootfs(ctx context.Context, app state.App, dep state.Deployment, acct state.Account,
	source *sourceBuildRootfsBinding, be storage.StorageBackend, key string, layers []io.Reader, manifest api.AppManifest) error {
	sbomKey, sbomRun := h.sbomStorageKeyForDeployment(ctx, dep.ID), h.syftRun
	if sbomKey == "" {
		sbomRun = nil
	}
	result, err := h.builder.BuildFullRootfs(ctx, rootfs.BuildFullRootfsInput{
		Layers:         layers,
		Manifest:       manifest,
		GuestInitPath:  h.guestInitPath,
		Plan:           acct.Plan,
		Storage:        be,
		StorageKey:     key,
		SBOMRun:        sbomRun,
		SBOMStorageKey: sbomKey,
	})
	if err != nil {
		return fmt.Errorf("imaged: build sandbox template rootfs: %w", err)
	}
	h.updateBuildProvenanceSBOM(ctx, dep.ID, result.SBOMKey)
	if err := h.publishSourceRootfs(ctx, app, dep, key, result, source); err != nil {
		return fmt.Errorf("imaged: stamp sandbox template rootfs: %w", err)
	}
	if err := h.replicateLayer(ctx, key); err != nil {
		return err
	}
	h.log.Info("imaged: build sandbox template rootfs", "app", app.Slug, "bytes", result.ContentBytes)
	return nil
}
