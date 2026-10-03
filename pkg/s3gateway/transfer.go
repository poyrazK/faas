package s3gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// One budget covers reading/staging and the provider request. Socket deadlines
// interrupt a stalled body read; cancellation also stops upstream forwarding.
// The daemon's server uses the same bound as a fallback for HTTP middleware
// which does not expose ResponseController's deadline operations.
func (h *Handler) boundTransfer(w http.ResponseWriter, r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), h.transferTimeout)
	deadline, _ := ctx.Deadline()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline.Add(api.ObjectUploadSettlementTimeout))
	return r.WithContext(ctx), cancel
}
