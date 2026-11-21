package imaged

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// announceLayerPublished tells schedd that a deployment's layer and its
// signature are stored and replicated (ADR-038 amendment 2026-11-17). The
// owning schedd verifies the layer while the scan runs, so the prime does not
// hash the whole layer before it boots. The notice is advisory: when it is
// lost or fails, the prime verifies the layer as before, so an error is
// logged and never fails the deployment.
func (h *Handler) announceLayerPublished(ctx context.Context, appID string, dep state.Deployment) {
	if h.notif == nil || dep.RootfsKey == "" {
		return
	}
	payload, err := json.Marshal(map[string]string{"app_id": appID, "deployment_id": dep.ID})
	if err == nil {
		err = h.notif.Notify(ctx, db.NotifyLayerPublished, string(payload))
	}
	if err != nil && h.log != nil {
		h.log.Warn("imaged: layer_published not sent; the prime verifies the layer", "deployment", dep.ID, "err", err)
	}
}
