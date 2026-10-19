package imaged

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

func captureSourceImagePublication(ctx context.Context, input state.SourceBuildRootfsInput) bool {
	p, ok := ctx.Value(imagePublicationKey{}).(*imagePublication)
	if !ok || p.deploymentID != input.DeploymentID {
		return false
	}
	p.path, p.key, p.bytes = input.RootfsPath, input.StorageKey, input.ContentBytes
	p.source = &input
	return true
}

func (h *Handler) publishPreparedImage(ctx context.Context, images state.DeploymentImagePreparationStore, prep state.ImagePreparation, publication *imagePublication) error {
	if publication.registry != nil {
		return h.publishPreparedRegistryImage(ctx, prep, *publication.registry)
	}
	if publication.source == nil {
		return images.PublishImagePreparationLayer(ctx, prep.DeploymentID, prep.ClaimToken, publication.path, publication.key, publication.bytes)
	}
	store, ok := h.store.(state.SourceImagePreparationStore)
	if !ok {
		return errors.New("imaged: atomic source image preparation store unavailable")
	}
	_, err := store.PublishSourceImagePreparationLayer(ctx, *publication.source, prep.ClaimToken)
	return err
}
