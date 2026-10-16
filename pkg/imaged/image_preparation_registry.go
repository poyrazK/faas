package imaged

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

func captureRegistryImagePublication(ctx context.Context, input state.DeploymentRegistryRootfsInput) bool {
	p, ok := ctx.Value(imagePublicationKey{}).(*imagePublication)
	if !ok || p.deploymentID != input.DeploymentID || input.WorkloadName != "" {
		return false
	}
	p.path, p.key, p.bytes = input.RootfsPath, input.StorageKey, input.ContentBytes
	p.registry = &input
	return true
}

func (h *Handler) publishPreparedRegistryImage(ctx context.Context, prep state.ImagePreparation, input state.DeploymentRegistryRootfsInput) error {
	store, ok := h.store.(state.RegistryImagePreparationStore)
	if !ok {
		return errors.New("imaged: atomic registry image preparation store unavailable")
	}
	_, err := store.PublishRegistryImagePreparationLayer(ctx, input, prep.ClaimToken)
	return err
}
