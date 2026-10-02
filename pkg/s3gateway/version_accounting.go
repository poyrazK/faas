package s3gateway

import (
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

// Current-object DELETE can create a retained marker. Decline it until marker
// admission and version-specific deletion can account for that extra storage.
func (h *Handler) allowCurrentObjectDelete(w http.ResponseWriter, r *http.Request, req requestContext, key string) bool {
	st, ok := h.store.(state.ObjectVersionInventoryStore)
	if !ok {
		return true
	}
	status, err := st.ObjectVersionAccountingStatus(r.Context(), req.bucket.AccountID, req.bucket.ID)
	if err != nil {
		h.providerError(w, r, req, err, key)
		return false
	}
	if status.Scope == state.ObjectInventoryAllVersions || status.VersionsObserved || status.NativeScanActive {
		h.providerError(w, r, req, objectstorage.ErrUnsupported, key)
		return false
	}
	return true
}
