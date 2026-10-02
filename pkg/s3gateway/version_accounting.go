package s3gateway

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) checkCopySource(w http.ResponseWriter, r *http.Request, req requestContext, key string, source objectstorage.CopySourceSnapshot, c objectstorage.CopySourceConditions) bool {
	if err := c.Check(source); err != nil {
		if errors.Is(err, objectstorage.ErrPreconditionFailed) {
			h.providerHTTPError(w, r, req, http.StatusPreconditionFailed, key)
		} else {
			h.providerError(w, r, req, err, key)
		}
		return false
	}
	if source.ProviderVersionID == "" || source.ProviderVersionID == "null" {
		return true
	}
	// A native source proves that current-object accounting is insufficient.
	// Admit its destination only against a verified retained-version baseline.
	st, ok := h.store.(state.ObjectVersionInventoryStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnsupported, key)
		return false
	}
	status, err := st.ObjectVersionAccountingStatus(r.Context(), req.bucket.AccountID, req.bucket.ID)
	if err != nil {
		h.providerError(w, r, req, err, key)
		return false
	}
	if status.Scope != state.ObjectInventoryAllVersions {
		h.providerError(w, r, req, objectstorage.ErrUnsupported, key)
		return false
	}
	return true
}

// Current-object DELETE can create a retained marker. Decline it until marker
// admission and version-specific deletion can account for that extra storage.
func (h *Handler) allowCurrentObjectDelete(w http.ResponseWriter, r *http.Request, req requestContext, key string) bool {
	if err := objectstorage.CheckCurrentObjectDelete(r.Context(), h.store, req.bucket); err != nil {
		h.providerError(w, r, req, err, key)
		return false
	}
	return true
}
