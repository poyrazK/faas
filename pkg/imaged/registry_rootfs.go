package imaged

// adr: 393

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

// Publication retains the verification belonging to this conversion and
// checks freshly read stored bytes against the builder's complete output.
func (h *Handler) publishContainerRootfs(ctx context.Context, app state.App, dep state.Deployment, prepared preparedContainerWorkload, workload, kind, key string, result rootfs.BuildResult) error {
	path := ""
	if workload == "" {
		path = h.appsRootPath(app.Slug, dep.ID)
	}
	if prepared.Verification.ID == "" {
		return h.publishUnsignedContainerRootfs(ctx, app, dep, prepared, workload, path, key, result)
	}
	store, ok := h.store.(state.DeploymentRegistryRootfsStore)
	if !ok {
		return fmt.Errorf("imaged: durable registry rootfs store unavailable")
	}
	parent := prepared.Verification
	if parent.Input.WorkloadName != workload || prepared.Reference != parent.Input.SelectedReference || prepared.Digest != parent.Input.SelectedDigest ||
		result.ImageKey != key || result.ArtifactDigest == "" || result.ArtifactBytes <= 0 {
		return fmt.Errorf("imaged: conversion artifact binding missing or mismatched")
	}
	if err := h.checkProducedRootfs(ctx, key, result); err != nil {
		return err
	}
	_, err := store.PublishDeploymentRegistryRootfs(ctx, state.DeploymentRegistryRootfsInput{
		ID: uuid.NewString(), RegistryVerificationID: parent.ID, RegistryInputHash: parent.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: workload,
		Scope: dep.Scope, Kind: kind, StorageKey: key, RootfsPath: path, ContentBytes: result.ContentBytes,
		ArtifactDigest: result.ArtifactDigest, ArtifactBytes: result.ArtifactBytes,
		LayerStart: prepared.LayerStart, Layers: prepared.Layers,
	})
	if err != nil {
		if errors.Is(err, cosign.ErrSignatureInvalid) {
			h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, parent.Input.SourceReference, "")
		}
		return fmt.Errorf("imaged: publish verified conversion: %w", err)
	}
	return nil
}

func (h *Handler) publishUnsignedContainerRootfs(ctx context.Context, app state.App, dep state.Deployment, prepared preparedContainerWorkload, workload, path, key string, result rootfs.BuildResult) error {
	if app.RequireSigned || app.SecurityPolicy.RequiresSignedImage() {
		return fmt.Errorf("imaged: missing conversion publisher verification")
	}
	if workload == "" {
		return h.setDeploymentRootfs(ctx, dep.ID, path, key, result.ContentBytes)
	}
	_, err := h.store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: dep.ID, SidecarName: workload, StorageKey: key, Bytes: result.ContentBytes, ContentDigest: prepared.Reference,
	})
	return err
}

func (h *Handler) checkProducedRootfs(ctx context.Context, key string, result rootfs.BuildResult) error {
	be, err := h.storageFor()
	if err != nil {
		return err
	}
	rc, err := be.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("imaged: read produced artifact: %w", err)
	}
	identity, readErr := rootfs.ReadArtifactIdentity(ctx, rc)
	closeErr := rc.Close()
	if readErr != nil {
		return fmt.Errorf("imaged: hash produced artifact: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("imaged: close produced artifact: %w", closeErr)
	}
	if identity.Digest != result.ArtifactDigest || identity.Bytes != result.ArtifactBytes {
		return fmt.Errorf("imaged: stored artifact differs from produced conversion")
	}
	return nil
}
