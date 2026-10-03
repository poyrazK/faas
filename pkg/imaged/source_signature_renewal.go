package imaged

// adr: 435. Renew exact retained build claims without reopening export archives.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) renewProducedSourceSignature(ctx context.Context, app state.App, dep state.Deployment) (bool, error) {
	roots, ok := h.store.(state.SourceBuildRootfsStore)
	if !ok {
		return false, nil
	}
	_, err := roots.GetCurrentSourceBuildRootfs(ctx, app.AccountID, app.ID, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	history, ok := h.store.(state.BuildExportPublicationHistoryStore)
	publications, supported := h.store.(state.BuildExportPublicationStore)
	if !ok || !supported {
		return true, fmt.Errorf("imaged: source publisher evidence unavailable")
	}
	proof, err := history.GetLatestBuildExportPublication(ctx, app.AccountID, app.ID, dep.ID, dep.BuildID)
	if err != nil {
		return true, err
	}
	proof.Input.ID = uuid.NewString()
	_, err = publications.RecordBuildExportPublication(ctx, proof.Input)
	return true, err
}
