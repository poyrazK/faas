package imaged

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func (h *Handler) deleteLayerArtifact(ctx context.Context, backend storage.StorageBackend, key string) error {
	claim, eligible, err := h.store.ClaimLayerArtifactDeletion(ctx, key)
	if err != nil {
		return fmt.Errorf("claim layer deletion: %w", err)
	}
	if !eligible {
		h.log.Info("imaged: retained referenced layer", "key", key)
		return nil
	}
	if err := backend.Delete(ctx, key); err != nil && !storage.IsNotFound(err) {
		return fmt.Errorf("delete claimed layer: %w", err)
	}
	if err := h.store.CompleteLayerArtifactDeletion(ctx, claim); err != nil {
		return fmt.Errorf("complete layer deletion: %w", err)
	}
	return nil
}

func (h *Handler) deleteDeploymentLayers(ctx context.Context, backend storage.StorageBackend, deployment state.Deployment, appSlug string) error {
	layers, err := h.store.ListDeploymentSidecarLayers(ctx, deployment.ID)
	if err != nil {
		return fmt.Errorf("list deployment layers for deletion: %w", err)
	}
	keys := map[string]bool{sched.AppLayerKey(appSlug, deployment.ID): true}
	if deployment.RootfsKey != "" {
		keys[deployment.RootfsKey] = true
	}
	for _, layer := range layers {
		keys[layer.StorageKey] = true
	}
	var ordered []string
	for key := range keys {
		if key != "" {
			ordered = append(ordered, key)
		}
	}
	sort.Strings(ordered)
	var failures []error
	for _, key := range ordered {
		if err := h.deleteLayerArtifact(ctx, backend, key); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (h *Handler) retryLayerArtifactDeletions(ctx context.Context) error {
	claims, err := h.store.PendingLayerArtifactDeletions(ctx)
	if err != nil || len(claims) == 0 {
		return err
	}
	backend, err := h.storageFor()
	if err != nil {
		return err
	}
	var failures []error
	for _, claim := range claims {
		if err := h.deleteLayerArtifact(ctx, backend, claim.StorageKey); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
