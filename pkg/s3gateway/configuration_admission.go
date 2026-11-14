package s3gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Customer configuration reads and preflights reserve each native attempt.
// Once intent is accepted, reconciliation uses the record-only meter so an
// exhausted budget cannot strand it. Cleanup also keeps that meter.
func (h *Handler) configurationRequestRecorder(bucket state.ObjectBucket) func(context.Context) error {
	return func(ctx context.Context) error {
		return objectstorage.RecordGatewayProviderRequest(ctx, h.requestMetrics, bucket.ID, h.now().UTC(), h.registry.Accounting)
	}
}

func (h *Handler) configurationAdmissionError(w http.ResponseWriter, r *http.Request, req requestContext, err error) bool {
	if errors.Is(err, state.ErrObjectBudget) || errors.Is(err, state.ErrObjectCapacity) || errors.Is(err, state.ErrObjectUsageStale) || errors.Is(err, state.ErrOrgAccessRevoked) {
		h.writeAdmissionError(w, r, req, err)
		return true
	}
	return false
}
